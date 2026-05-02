package instance

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRandomizeHostPorts_ClearsHostPort verifies that bindings with explicit
// HostPort values get cleared (so dockerd will assign random ports on start).
func TestRandomizeHostPorts_ClearsHostPort(t *testing.T) {
	tmpDir := t.TempDir()
	containerID := "abcdef0123456789aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	containerDir := filepath.Join(tmpDir, "containers", containerID)
	require.NoError(t, os.MkdirAll(containerDir, 0755))

	hostconfig := map[string]interface{}{
		"PortBindings": map[string]interface{}{
			"6443/tcp": []interface{}{
				map[string]interface{}{"HostIp": "0.0.0.0", "HostPort": "34567"},
			},
			"80/tcp": []interface{}{
				map[string]interface{}{"HostIp": "0.0.0.0", "HostPort": "8080"},
			},
		},
	}
	data, _ := json.Marshal(hostconfig)
	require.NoError(t, os.WriteFile(filepath.Join(containerDir, "hostconfig.json"), data, 0644))

	mgr := &Manager{logger: slog.Default()}
	mgr.randomizeHostPorts(tmpDir, slog.Default())

	patched, err := os.ReadFile(filepath.Join(containerDir, "hostconfig.json"))
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(patched, &got))

	pb := got["PortBindings"].(map[string]interface{})
	for port, bindings := range pb {
		bindList := bindings.([]interface{})
		for _, b := range bindList {
			binding := b.(map[string]interface{})
			assert.Equal(t, "", binding["HostPort"],
				"HostPort for %s should have been cleared", port)
		}
	}
}

// TestRandomizeHostPorts_TolerantOfShortDirectoryNames verifies the regression
// fix where entry.Name()[:12] used to panic on short names.
func TestRandomizeHostPorts_TolerantOfShortDirectoryNames(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a containers/ subdir with a name shorter than 12 chars.
	shortDir := filepath.Join(tmpDir, "containers", "short")
	require.NoError(t, os.MkdirAll(shortDir, 0755))

	hostconfig := map[string]interface{}{
		"PortBindings": map[string]interface{}{
			"80/tcp": []interface{}{
				map[string]interface{}{"HostIp": "0.0.0.0", "HostPort": "8080"},
			},
		},
	}
	data, _ := json.Marshal(hostconfig)
	require.NoError(t, os.WriteFile(filepath.Join(shortDir, "hostconfig.json"), data, 0644))

	// Must not panic even though dir name is < 12 chars.
	mgr := &Manager{logger: slog.Default()}
	assert.NotPanics(t, func() {
		mgr.randomizeHostPorts(tmpDir, slog.Default())
	})
}

// TestRandomizeHostPorts_SkipsEmptyHostPort — bindings already in
// "let docker pick" state shouldn't be rewritten (avoid pointless disk writes).
func TestRandomizeHostPorts_SkipsEmptyHostPort(t *testing.T) {
	tmpDir := t.TempDir()
	containerDir := filepath.Join(tmpDir, "containers", "aaaaaaaaaaaa-noop")
	require.NoError(t, os.MkdirAll(containerDir, 0755))

	hostconfig := map[string]interface{}{
		"PortBindings": map[string]interface{}{
			"80/tcp": []interface{}{
				map[string]interface{}{"HostIp": "0.0.0.0", "HostPort": ""},
			},
		},
	}
	data, _ := json.Marshal(hostconfig)
	hcPath := filepath.Join(containerDir, "hostconfig.json")
	require.NoError(t, os.WriteFile(hcPath, data, 0644))

	infoBefore, err := os.Stat(hcPath)
	require.NoError(t, err)

	mgr := &Manager{logger: slog.Default()}
	mgr.randomizeHostPorts(tmpDir, slog.Default())

	infoAfter, err := os.Stat(hcPath)
	require.NoError(t, err)
	assert.Equal(t, infoBefore.ModTime(), infoAfter.ModTime(),
		"file should not have been rewritten when no bindings needed clearing")
}

// TestRandomizeHostPorts_HandlesMissingDir — missing data-root must not panic.
func TestRandomizeHostPorts_HandlesMissingDir(t *testing.T) {
	mgr := &Manager{logger: slog.Default()}
	assert.NotPanics(t, func() {
		mgr.randomizeHostPorts("/nonexistent/path", slog.Default())
	})
}

// TestShortID covers the helper used throughout the instance package.
func TestShortID(t *testing.T) {
	assert.Equal(t, "abc", shortID("abc"))
	assert.Equal(t, "abcdefghijkl", shortID("abcdefghijkl"))
	assert.Equal(t, "abcdefghijkl", shortID("abcdefghijklm")) // truncated to 12
	assert.Equal(t, "", shortID(""))
}
