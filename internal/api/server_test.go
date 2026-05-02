package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/internal/instance"
	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/internal/zfs"
)

// fakeDockerd is a tiny DockerdManager mock for API tests.
type fakeDockerd struct {
	mu       sync.Mutex
	running  map[string]bool
	startErr error
	stopErr  error
}

func newFakeDockerd() *fakeDockerd {
	return &fakeDockerd{running: make(map[string]bool)}
}

func (f *fakeDockerd) Start(_ context.Context, name, _, _, _, _ string, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.running[name] = true
	return nil
}

func (f *fakeDockerd) Stop(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.running[name] = false
	return nil
}

func (f *fakeDockerd) nameFromPidFile(pidFile string) string {
	base := filepath.Base(pidFile)
	return strings.TrimSuffix(base, ".pid")
}

func (f *fakeDockerd) IsRunning(pidFile string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[f.nameFromPidFile(pidFile)]
}

func (f *fakeDockerd) IsRunningBatch(pidFiles []string) map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]bool, len(pidFiles))
	for _, pf := range pidFiles {
		out[pf] = f.running[f.nameFromPidFile(pf)]
	}
	return out
}

func (f *fakeDockerd) RemoveConfig(name string) error { return nil }

// testServer builds a fully wired API server backed by in-memory mocks plus
// returns an httptest.Server pointed at it. The token argument enables auth
// when non-empty.
func testServer(t *testing.T, token string) (*httptest.Server, *instance.Manager) {
	t.Helper()
	tmpDir := t.TempDir()

	cfg := config.Defaults()
	cfg.StateFile = filepath.Join(tmpDir, "state.json")
	cfg.RunDir = filepath.Join(tmpDir, "run")
	cfg.API.Token = token

	mgr, err := instance.NewManagerWithOpts(cfg, instance.ManagerOpts{
		ZFS:     zfs.NewMock(),
		Dockerd: newFakeDockerd(),
		State:   state.NewStore(cfg.StateFile),
		Logger:  slog.Default(),
	})
	require.NoError(t, err)

	srv := NewServer(cfg, mgr, slog.Default())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, mgr
}

// doReq is a tiny HTTP helper for tests.
func doReq(t *testing.T, method, url, token string, body interface{}, accept string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, url, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := ts().Do(req)
	require.NoError(t, err)
	return resp
}

// ts returns the default *http.Client; pulled out so doReq can be retargeted in tests if needed.
func ts() *http.Client { return http.DefaultClient }

func decodeJSON(t *testing.T, resp *http.Response, into interface{}) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(into))
}

func TestAPI_Health_NoAuth(t *testing.T) {
	srv, _ := testServer(t, "supersecret")

	resp, err := http.Get(srv.URL + "/api/v1/health")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode,
		"health must be reachable without a token even when auth is configured")

	var health map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&health))
	assert.Equal(t, "ok", health["status"])
	assert.Contains(t, health, "uptime")
	assert.Contains(t, health, "started_at")
}

func TestAPI_TokenAuth(t *testing.T) {
	srv, _ := testServer(t, "secret-token")

	t.Run("missing token", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/api/v1/instances")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("wrong token", func(t *testing.T) {
		resp := doReq(t, "GET", srv.URL+"/api/v1/instances", "wrong", nil, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("correct token", func(t *testing.T) {
		resp := doReq(t, "GET", srv.URL+"/api/v1/instances", "secret-token", nil, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestAPI_NoToken_AllowsAllRequests(t *testing.T) {
	srv, _ := testServer(t, "") // no token configured

	resp, err := http.Get(srv.URL + "/api/v1/instances")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAPI_CreateInstance(t *testing.T) {
	srv, _ := testServer(t, "")

	t.Run("happy path", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
			map[string]string{"name": "env1"}, "")
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		var inst state.Instance
		decodeJSON(t, resp, &inst)
		assert.Equal(t, "env1", inst.Name)
		assert.Equal(t, state.StatusRunning, inst.Status)
	})

	t.Run("invalid name rejected", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
			map[string]string{"name": "../escape"}, "")
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp map[string]string
		decodeJSON(t, resp, &errResp)
		assert.Contains(t, errResp["error"], "invalid instance name")
	})

	t.Run("missing name rejected", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
			map[string]string{}, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("malformed JSON rejected", func(t *testing.T) {
		req, err := http.NewRequest("POST", srv.URL+"/api/v1/instances",
			strings.NewReader("not json"))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestAPI_DeleteMissingInstance_Returns404(t *testing.T) {
	srv, _ := testServer(t, "")

	resp := doReq(t, "DELETE", srv.URL+"/api/v1/instances/missing", "", nil, "")
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"DELETE on a missing instance must return 404, not 500")
}

func TestAPI_GetAndDeleteInstance(t *testing.T) {
	srv, _ := testServer(t, "")

	resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
		map[string]string{"name": "env1"}, "")
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	t.Run("get found", func(t *testing.T) {
		resp := doReq(t, "GET", srv.URL+"/api/v1/instances/env1", "", nil, "")
		var inst state.Instance
		decodeJSON(t, resp, &inst)
		assert.Equal(t, "env1", inst.Name)
	})

	t.Run("get not found", func(t *testing.T) {
		resp := doReq(t, "GET", srv.URL+"/api/v1/instances/missing", "", nil, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("delete then 404", func(t *testing.T) {
		resp := doReq(t, "DELETE", srv.URL+"/api/v1/instances/env1", "", nil, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusNoContent, resp.StatusCode)

		resp = doReq(t, "GET", srv.URL+"/api/v1/instances/env1", "", nil, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestAPI_StreamingProgress(t *testing.T) {
	srv, _ := testServer(t, "")

	// Create instance first.
	resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
		map[string]string{"name": "env1"}, "")
	resp.Body.Close()

	// Take a snapshot, requesting NDJSON streaming.
	req, err := http.NewRequest("POST",
		srv.URL+"/api/v1/instances/env1/snapshot",
		bytes.NewReader([]byte(`{"label":"golden"}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")

	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))

	// Read all events. Each line is a JSON event.
	var events []instance.ProgressEvent
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var ev instance.ProgressEvent
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &ev))
		events = append(events, ev)
	}

	require.NotEmpty(t, events, "expected at least one progress event")
	assert.Equal(t, "complete", events[len(events)-1].Step,
		"final event should be the 'complete' marker")
	assert.Equal(t, "done", events[len(events)-1].Status)
}

func TestAPI_NonStreamingFallsBackToSyncJSON(t *testing.T) {
	srv, _ := testServer(t, "")

	resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
		map[string]string{"name": "env1"}, "")
	resp.Body.Close()

	// No Accept header — should get the legacy sync JSON shape.
	resp = doReq(t, "POST", srv.URL+"/api/v1/instances/env1/snapshot", "",
		map[string]string{"label": "golden"}, "")
	defer resp.Body.Close()

	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var body map[string]string
	decodeJSON(t, resp, &body)
	assert.Equal(t, "ok", body["status"])
}

func TestAPI_SnapshotErrorPaths(t *testing.T) {
	srv, _ := testServer(t, "")

	t.Run("missing label", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances/anything/snapshot", "",
			map[string]string{}, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestAPI_RevertErrorPaths(t *testing.T) {
	srv, _ := testServer(t, "")

	t.Run("missing label", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances/anything/revert", "",
			map[string]string{}, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestAPI_CloneErrorPaths(t *testing.T) {
	srv, _ := testServer(t, "")

	t.Run("missing label", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances/src/clone", "",
			map[string]string{"new_name": "clone"}, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("invalid new name", func(t *testing.T) {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances/src/clone", "",
			map[string]string{"label": "snap", "new_name": "../bad"}, "")
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestAPI_ListInstances(t *testing.T) {
	srv, _ := testServer(t, "")

	for _, name := range []string{"env1", "env2", "env3"} {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
			map[string]string{"name": name}, "")
		resp.Body.Close()
		require.Equal(t, http.StatusCreated, resp.StatusCode)
	}

	resp := doReq(t, "GET", srv.URL+"/api/v1/instances", "", nil, "")
	var instances []*state.Instance
	decodeJSON(t, resp, &instances)
	assert.Len(t, instances, 3)
}

func TestAPI_HealthReportsRunningCount(t *testing.T) {
	srv, _ := testServer(t, "")

	for _, name := range []string{"env1", "env2"} {
		resp := doReq(t, "POST", srv.URL+"/api/v1/instances", "",
			map[string]string{"name": name}, "")
		resp.Body.Close()
	}

	resp := doReq(t, "POST", srv.URL+"/api/v1/instances/env1/stop", "", nil, "")
	resp.Body.Close()

	resp = doReq(t, "GET", srv.URL+"/api/v1/health", "", nil, "")
	var health map[string]interface{}
	decodeJSON(t, resp, &health)

	assert.Equal(t, float64(2), health["instances"])
	assert.Equal(t, float64(1), health["running"], "expected 1 running after stop")
}

