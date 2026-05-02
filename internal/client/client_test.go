package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/instance"
	"github.com/johnbuluba/dockersnap/internal/state"
)

// fakeServer mounts a tiny fake API on httptest.Server so the client can be
// exercised against real HTTP traffic.
func fakeServer(t *testing.T, h http.Handler) (*httptest.Server, *Client) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	c := New(ts.URL, "")
	return ts, c
}

func TestClient_Health(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"uptime":     "5m",
			"started_at": now,
			"instances":  3,
			"running":    2,
		})
	})

	_, c := fakeServer(t, mux)

	got, err := c.Health(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", got.Status)
	assert.Equal(t, "5m", got.Uptime)
	assert.Equal(t, 3, got.Instances)
	assert.Equal(t, 2, got.Running)
}

func TestClient_TokenSentInHeader(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]state.Instance{})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	c := New(ts.URL, "supersecret")

	_, err := c.List(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Bearer supersecret", got)
}

func TestClient_NoTokenOmitsHeader(t *testing.T) {
	mux := http.NewServeMux()
	var got string
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]state.Instance{})
	})

	_, c := fakeServer(t, mux)

	_, err := c.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got, "client must not send Authorization when token is empty")
}

func TestClient_DecodeAPIError_JSONErrorField(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "ZFS pool offline"})
	})

	_, c := fakeServer(t, mux)
	_, err := c.List(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API error (500)")
	assert.Contains(t, err.Error(), "ZFS pool offline")
}

func TestClient_DecodeAPIError_NonJSONBodyExcerpt(t *testing.T) {
	// Server returns an HTML error page (e.g. from a reverse proxy in front of dockersnap).
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html><body>502 Bad Gateway</body></html>")
	})

	_, c := fakeServer(t, mux)
	_, err := c.List(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 502")
	assert.Contains(t, err.Error(), "Bad Gateway",
		"non-JSON error bodies should be surfaced as an excerpt")
}

func TestClient_DecodeAPIError_LongBodyTruncated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, strings.Repeat("X", 1000))
	})

	_, c := fakeServer(t, mux)
	_, err := c.List(context.Background())
	require.Error(t, err)
	// We cap excerpts at 200 chars + "..." for readability.
	assert.True(t, strings.HasSuffix(err.Error(), "..."),
		"long error bodies should be truncated with a ... marker")
}

func TestClient_Create(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "env1", req["name"])
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(state.Instance{Name: "env1", Status: state.StatusRunning})
	})

	_, c := fakeServer(t, mux)
	inst, err := c.Create(context.Background(), "env1", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "env1", inst.Name)
	assert.Equal(t, state.StatusRunning, inst.Status)
}

func TestClient_NoContentResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances/env1", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent)
	})

	_, c := fakeServer(t, mux)
	require.NoError(t, c.Delete(context.Background(), "env1"))
}

func TestClient_StreamProgress(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances/env1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/x-ndjson", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		events := []instance.ProgressEvent{
			{Step: "stopping_dockerd", Status: "running", Message: "Stopping..."},
			{Step: "stopping_dockerd", Status: "done", Message: "Stopped"},
			{Step: "complete", Status: "done", Message: "Snapshot 'golden' created"},
		}
		for _, e := range events {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "%s\n", data)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	_, c := fakeServer(t, mux)

	var got []instance.ProgressEvent
	err := c.SnapshotStream(context.Background(), "env1", "golden", nil,
		func(ev instance.ProgressEvent) { got = append(got, ev) })
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "complete", got[2].Step)
}

func TestClient_StreamProgress_PropagatesErrorEvent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances/env1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)

		flusher, _ := w.(http.Flusher)
		events := []instance.ProgressEvent{
			{Step: "zfs_snapshot", Status: "error", Message: "pool offline"},
		}
		for _, e := range events {
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "%s\n", data)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	_, c := fakeServer(t, mux)

	err := c.SnapshotStream(context.Background(), "env1", "golden", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pool offline",
		"error events should propagate as the call's return error")
}

func TestClient_StreamProgress_FallsBackOnNonStreamingResponse(t *testing.T) {
	// Server returns plain JSON (not NDJSON). Client should treat as success.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances/env1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	_, c := fakeServer(t, mux)

	called := false
	err := c.SnapshotStream(context.Background(), "env1", "golden", nil,
		func(instance.ProgressEvent) { called = true })
	require.NoError(t, err)
	assert.False(t, called, "no events should be emitted when server doesn't stream")
}

func TestClient_Access(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances/env1/access", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"contract_version": "1",
			"env": map[string]string{
				"KUBECONFIG": "${ACCESS_DIR}/kubeconfig",
			},
			"files": []map[string]interface{}{
				{"name": "kubeconfig", "content": "server: https://h:1", "mode": "0600"},
			},
		})
	})

	_, c := fakeServer(t, mux)
	got, err := c.Access(context.Background(), "env1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "${ACCESS_DIR}/kubeconfig", got.Env["KUBECONFIG"])
	require.Len(t, got.Files, 1)
	assert.Equal(t, "kubeconfig", got.Files[0].Name)
}

func TestClient_Workload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances/env1/workload", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workload_type": "kind",
			"status":        "ready",
		})
	})

	_, c := fakeServer(t, mux)
	got, err := c.Workload(context.Background(), "env1")
	require.NoError(t, err)
	assert.Equal(t, "kind", got["workload_type"])
}

func TestClient_WorkloadHealth_FreshFlag(t *testing.T) {
	mux := http.NewServeMux()
	var gotFresh string
	mux.HandleFunc("/api/v1/instances/env1/workload/health", func(w http.ResponseWriter, r *http.Request) {
		gotFresh = r.URL.Query().Get("fresh")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"healthy": true})
	})

	_, c := fakeServer(t, mux)

	_, err := c.WorkloadHealth(context.Background(), "env1", false)
	require.NoError(t, err)
	assert.Equal(t, "", gotFresh)

	_, err = c.WorkloadHealth(context.Background(), "env1", true)
	require.NoError(t, err)
	assert.Equal(t, "true", gotFresh)
}

func TestClient_Plugins(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/plugins", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"name": "kind", "status": "ready", "version": "1.0.0"},
		})
	})

	_, c := fakeServer(t, mux)
	got, err := c.Plugins(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "kind", got[0].Name)
	assert.Equal(t, "ready", got[0].Status)
}

func TestClient_ReloadPlugins(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/plugins/reload", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
	})

	_, c := fakeServer(t, mux)
	got, err := c.ReloadPlugins(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestClient_CreateWithInlineWorkload(t *testing.T) {
	mux := http.NewServeMux()
	var gotBody map[string]interface{}
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"name": gotBody["name"],
		})
	})

	_, c := fakeServer(t, mux)
	_, err := c.Create(context.Background(), "demo", &WorkloadInline{
		Plugin: "kind",
		Config: map[string]interface{}{"retries": 5},
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "demo", gotBody["name"])
	require.Contains(t, gotBody, "workload_inline")
	inline := gotBody["workload_inline"].(map[string]interface{})
	assert.Equal(t, "kind", inline["plugin"])
}

func TestClient_ContextCancellation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		// Block until the client cancels.
		<-r.Context().Done()
	})

	_, c := fakeServer(t, mux)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	_, err := c.List(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}
