//go:build integration

package kind

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// These tests exercise the kind plugin through dockersnap's plugin
// pipeline: `dockersnap create --workload kind-default` deploys, the
// /access endpoint produces a kubeconfig, and kubectl talks to the
// cluster from outside the netns through the daemon's TCP proxy.
//
// Existing tests in kind_test.go shell out to `kind create cluster`
// directly and bypass the plugin — they're kept because they exercise
// kind itself, but these tests are what validates that *our* plumbing
// works.

const (
	// Kind cluster + plugin deploy can run long behind the corporate proxy
	// (image pull, control-plane start, --wait).
	kindDeployTimeout = 4 * time.Minute
	kindReadyTimeout  = 3 * time.Minute
)

// TestKindPlugin_IsLoaded confirms the daemon discovered the kind plugin.
// Cheap pre-check; the rest of the suite is moot if this fails.
func TestKindPlugin_IsLoaded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	plugins, err := c.Plugins(ctx)
	require.NoError(t, err)

	var got *client.PluginInfo
	for i, p := range plugins {
		if p.Name == "kind" {
			got = &plugins[i]
			break
		}
	}
	require.NotNil(t, got, "kind plugin not discovered: %+v", plugins)
	assert.Equal(t, "ready", got.Status, "kind plugin not ready: %s", got.Error)
	assert.NotEmpty(t, got.Version)
}

// TestKindPlugin_DeployAndKubeconfig is the core happy-path test. It
// validates that the full pipeline works end-to-end: deploy through the
// plugin, /access produces a kubeconfig with a reachable URL, and kubectl
// can talk to the cluster from outside the netns.
func TestKindPlugin_DeployAndKubeconfig(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), kindDeployTimeout+kindReadyTimeout)
	defer cancel()

	name := instName("kc")
	cleanup(t, name)
	defer cleanup(t, name)

	createKindInstance(t, ctx, name)

	kubeconfigPath := materializeKubeconfig(t, ctx, name)

	// Cluster takes a moment to be ready even after deploy returns.
	waitForKubectl(t, kubeconfigPath, kindReadyTimeout, "get", "nodes")

	out := kubectl(t, kubeconfigPath, "get", "nodes",
		"-o", `jsonpath={.items[*].status.conditions[?(@.type=="Ready")].status}`)
	assert.Contains(t, out, "True", "expected at least one Ready node, got: %s", out)

	// The kubeconfig URL must NOT be 127.0.0.1 — it should point at whatever
	// host the test client used to reach the daemon (loopback in this case
	// since the test runs on the VM, but resolved via the request Host header).
	kc := readFile(t, kubeconfigPath)
	assert.NotContains(t, kc, "https://127.0.0.1:6443",
		"kubeconfig must rewrite kind's literal 127.0.0.1:6443 to the proxy address")
	assert.Contains(t, kc, "insecure-skip-tls-verify",
		"patched kubeconfig must use insecure-skip-tls-verify")
}

// TestKindPlugin_Health asserts the workload health endpoint reports
// healthy after the cluster is up. Uses ?fresh=true to bypass the cache.
func TestKindPlugin_Health(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), kindDeployTimeout+kindReadyTimeout)
	defer cancel()

	name := instName("hc")
	cleanup(t, name)
	defer cleanup(t, name)

	createKindInstance(t, ctx, name)

	// Health may take an extra moment after deploy returns — kubelet finishes
	// rolling system pods. Poll up to 2 minutes with ?fresh=true.
	deadline := time.Now().Add(2 * time.Minute)
	var lastResp map[string]interface{}
	for time.Now().Before(deadline) {
		resp, err := c.WorkloadHealth(ctx, name, true)
		require.NoError(t, err, "WorkloadHealth call")
		lastResp = resp
		if h, _ := resp["healthy"].(bool); h {
			t.Logf("✓ workload healthy: %v", resp)
			return
		}
		time.Sleep(3 * time.Second)
	}
	t.Fatalf("workload never became healthy: %v", lastResp)
}

// TestKindPlugin_SnapshotRevertPreservesState is the consistency test that
// matters most for kind: ZFS snapshot of a running cluster, mutate state,
// revert, verify the rollback restored cluster state correctly. Validates
// that etcd survives ZFS rollback and that port forwarding + kubeconfig
// re-establish after dockerd restart.
func TestKindPlugin_SnapshotRevertPreservesState(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(),
		kindDeployTimeout+kindReadyTimeout+5*time.Minute)
	defer cancel()

	name := instName("sr")
	cleanup(t, name)
	defer cleanup(t, name)

	createKindInstance(t, ctx, name)
	kubeconfigPath := materializeKubeconfig(t, ctx, name)
	waitForKubectl(t, kubeconfigPath, kindReadyTimeout, "get", "nodes")

	t.Log("creating namespace ns-before-snap (should survive revert)")
	kubectl(t, kubeconfigPath, "create", "namespace", "ns-before-snap")

	t.Log("snapshotting…")
	require.NoError(t, c.Snapshot(ctx, name, "golden", nil), "Snapshot")

	// Cluster needs to come back from the dockerd restart triggered by
	// snapshot. The kubeconfig URL changes (new host port from the proxy)
	// so we re-fetch.
	kubeconfigPath = materializeKubeconfig(t, ctx, name)
	waitForKubectl(t, kubeconfigPath, kindReadyTimeout, "get", "nodes")

	t.Log("creating namespace ns-after-snap (should be gone after revert)")
	kubectl(t, kubeconfigPath, "create", "namespace", "ns-after-snap")
	out := kubectl(t, kubeconfigPath, "get", "namespace", "ns-after-snap", "-o", "name")
	require.Contains(t, out, "ns-after-snap", "namespace creation must succeed before revert")

	t.Log("reverting to golden")
	require.NoError(t, c.Revert(ctx, name, "golden", true), "Revert")

	kubeconfigPath = materializeKubeconfig(t, ctx, name)
	waitForKubectl(t, kubeconfigPath, kindReadyTimeout, "get", "nodes")

	// ns-before-snap must survive (was there at snapshot time).
	out = kubectl(t, kubeconfigPath, "get", "namespace", "ns-before-snap", "-o", "name")
	assert.Contains(t, out, "ns-before-snap",
		"ns-before-snap must survive revert (existed at snapshot time)")

	// ns-after-snap must be gone (created after the snapshot).
	out = kubectl(t, kubeconfigPath, "get", "namespace", "ns-after-snap",
		"--ignore-not-found", "-o", "name")
	assert.NotContains(t, out, "ns-after-snap",
		"ns-after-snap must be gone after revert — etcd should have rolled back")
}

// TestKindPlugin_KubeconfigSurvivesDaemonRestart restarts dockersnap and
// proves the cluster is still reachable. Exercises live-restore +
// reconcile + port-forwarding reattach + cached kubeconfig still resolves
// to a valid (re-allocated) host port.
// Intentionally NOT parallel: this test runs `systemctl restart dockersnap`,
// which would kill any in-flight operations from concurrent tests. Keep
// serial. Same constraint applies to TestEdge_ReconcileAfterDaemonRestart
// in tests/e2e/.
func TestKindPlugin_KubeconfigSurvivesDaemonRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(),
		kindDeployTimeout+kindReadyTimeout+2*time.Minute)
	defer cancel()

	name := instName("rst")
	cleanup(t, name)
	defer cleanup(t, name)

	createKindInstance(t, ctx, name)
	kc := materializeKubeconfig(t, ctx, name)
	waitForKubectl(t, kc, kindReadyTimeout, "get", "nodes")

	t.Log("restarting dockersnap daemon")
	require.NoError(t, exec.Command("systemctl", "restart", "dockersnap").Run())

	// Wait for API to come back.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.List(ctx); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Re-fetch kubeconfig — the port forward may have a new host port.
	kc = materializeKubeconfig(t, ctx, name)

	// kubectl should still work, possibly after a brief wait while reconcile
	// re-establishes port forwarding.
	waitForKubectl(t, kc, 90*time.Second, "get", "nodes")

	out := kubectl(t, kc, "get", "namespace", "kube-system", "-o", "name")
	assert.Contains(t, out, "kube-system",
		"kube-system namespace should be readable after daemon restart")
}

// TestKindPlugin_CloneIsIndependent creates a source cluster, snapshots,
// clones, and verifies both run in parallel with independent etcd state.
// The cluster on the clone is reachable, has the source's pre-snapshot
// state, and changes on the clone don't bleed into the source.
func TestKindPlugin_CloneIsIndependent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(),
		2*kindDeployTimeout+2*kindReadyTimeout+5*time.Minute)
	defer cancel()

	srcName := instName("src")
	cloneName := instName("clone")
	cleanup(t, srcName)
	cleanup(t, cloneName)
	defer cleanup(t, srcName)
	defer cleanup(t, cloneName)

	createKindInstance(t, ctx, srcName)
	srcKC := materializeKubeconfig(t, ctx, srcName)
	waitForKubectl(t, srcKC, kindReadyTimeout, "get", "nodes")

	t.Log("creating ns-on-source on the source cluster (should propagate to clone)")
	kubectl(t, srcKC, "create", "namespace", "ns-on-source")

	t.Log("snapshotting source")
	require.NoError(t, c.Snapshot(ctx, srcName, "for-clone", nil))

	srcKC = materializeKubeconfig(t, ctx, srcName)
	waitForKubectl(t, srcKC, kindReadyTimeout, "get", "nodes")

	t.Log("cloning source@for-clone → " + cloneName)
	_, err := c.Clone(ctx, srcName, "for-clone", cloneName)
	require.NoError(t, err, "Clone")

	cloneKC := materializeKubeconfig(t, ctx, cloneName)
	waitForKubectl(t, cloneKC, kindReadyTimeout, "get", "nodes")

	// Clone has the namespace inherited from the source's snapshot.
	out := kubectl(t, cloneKC, "get", "namespace", "ns-on-source", "-o", "name")
	assert.Contains(t, out, "ns-on-source",
		"clone must have ns-on-source inherited from source's snapshot")

	// Mutate clone, source must NOT see it.
	t.Log("creating ns-on-clone on the clone (should NOT appear on source)")
	kubectl(t, cloneKC, "create", "namespace", "ns-on-clone")

	out = kubectl(t, cloneKC, "get", "namespace", "ns-on-clone", "-o", "name")
	require.Contains(t, out, "ns-on-clone")

	out = kubectl(t, srcKC, "get", "namespace", "ns-on-clone", "--ignore-not-found", "-o", "name")
	assert.NotContains(t, out, "ns-on-clone",
		"clone-only namespace must not bleed into source")

	// Both clusters must still be reachable (parallel execution).
	out = kubectl(t, srcKC, "get", "nodes",
		"-o", `jsonpath={.items[*].status.conditions[?(@.type=="Ready")].status}`)
	assert.Contains(t, out, "True", "source nodes must still be Ready")
	out = kubectl(t, cloneKC, "get", "nodes",
		"-o", `jsonpath={.items[*].status.conditions[?(@.type=="Ready")].status}`)
	assert.Contains(t, out, "True", "clone nodes must still be Ready")
}

// TestKindPlugin_TeardownRemovesCluster verifies that Delete removes the
// kind cluster from the instance's docker daemon. Catches plugin-teardown
// regressions where the cluster would leak when the dataset gets destroyed.
func TestKindPlugin_TeardownRemovesCluster(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), kindDeployTimeout+kindReadyTimeout)
	defer cancel()

	name := instName("td")
	cleanup(t, name)
	// Belt-and-braces: if any assertion before the explicit Delete below
	// fails, the deferred cleanup runs Delete anyway. cleanup() tolerates
	// "instance not found" so there's no false-negative when the
	// in-test Delete already succeeded.
	defer cleanup(t, name)

	createKindInstance(t, ctx, name)

	// `kind get clusters` against the instance's docker daemon should report
	// at least one cluster.
	socket := cfg.SocketPath(name)
	got := runKindBin(t, socket, "get", "clusters")
	require.Contains(t, got, name,
		"kind cluster %q should be present before delete; got %s", name, got)

	// Delete via the API — this runs the plugin's teardown.
	require.NoError(t, c.Delete(ctx, name), "Delete")

	// The instance is gone, so `dockersnap docker <name>` won't work.
	// Instead we verify the cluster doesn't show up in any docker daemon
	// the host might still have, by checking that the dockerd socket is gone.
	_, err := os.Stat(socket)
	assert.True(t, os.IsNotExist(err), "socket %s should be gone after Delete", socket)
}

// --- helpers ---

// createKindInstance creates an instance with the kind plugin bound inline
// (no preset). Fails the test with a useful message on plugin errors.
func createKindInstance(t *testing.T, ctx context.Context, name string) {
	t.Helper()
	t.Logf("creating kind instance %q (this can take a couple of minutes)…", name)
	_, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "kind"}, nil)
	require.NoError(t, err, "Create with --plugin kind")
}

// materializeKubeconfig calls /access, materializes Files[] under a
// per-test temp dir, and returns the path to the kubeconfig file.
func materializeKubeconfig(t *testing.T, ctx context.Context, name string) string {
	t.Helper()

	access, err := c.Access(ctx, name)
	require.NoError(t, err, "Access")
	require.NotEmpty(t, access.Files, "Access must return at least one file (kubeconfig)")

	dir := filepath.Join(t.TempDir(), "access")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	var path string
	for _, f := range access.Files {
		filePath := filepath.Join(dir, f.Name)
		mode, _ := strconv.ParseUint(f.Mode, 8, 32)
		if mode == 0 {
			mode = 0o600
		}
		// CLI substitutes ${ACCESS_DIR} locally.
		content := strings.ReplaceAll(f.Content, pluginsdk.AccessDirToken, dir)
		require.NoError(t, os.WriteFile(filePath, []byte(content), os.FileMode(mode)))
		if f.Name == "kubeconfig" {
			path = filePath
		}
	}
	require.NotEmpty(t, path, "Access response missing 'kubeconfig' file: got %+v", access.Files)
	return path
}

// kubectl runs kubectl with --kubeconfig=<path> and returns combined output.
// Failures don't fail the test (they're returned in the output) — callers
// inspect the output to decide whether the call succeeded.
func kubectl(t *testing.T, kubeconfigPath string, args ...string) string {
	t.Helper()
	full := append([]string{"--kubeconfig", kubeconfigPath, "--request-timeout=10s"}, args...)
	cmd := exec.Command("kubectl", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("kubectl %v: %v\n  output: %s", args, err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

// waitForKubectl polls kubectl <args> until it returns success (exit 0)
// or the timeout fires. Used to wait for the API server to be reachable
// after deploy / snapshot / revert / daemon restart.
func waitForKubectl(t *testing.T, kubeconfigPath string, timeout time.Duration, args ...string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		full := append([]string{"--kubeconfig", kubeconfigPath, "--request-timeout=5s"}, args...)
		cmd := exec.Command("kubectl", full...)
		if err := cmd.Run(); err == nil {
			t.Logf("kubectl %v ready (attempt %d)", args, attempt)
			return
		}
		time.Sleep(2 * time.Second)
	}
	require.Failf(t, "kubectl never became responsive",
		"kubectl %v did not succeed within %s (%d attempts)", args, timeout, attempt)
}

// runKindBin shells out to the kind binary against a specific docker socket.
// Used by TestKindPlugin_TeardownRemovesCluster to query cluster presence
// without going through the dockersnap CLI (which the test is mid-deleting).
func runKindBin(t *testing.T, socket string, args ...string) string {
	t.Helper()
	cmd := exec.Command("kind", args...)
	cmd.Env = append(os.Environ(), pluginsdk.DockerHostEnv(socket))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("kind %v: %v output: %s", args, err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

// readFile is a thin helper for the kubeconfig content assertion.
func readFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	b, err := io.ReadAll(f)
	require.NoError(t, err)
	return string(b)
}

// fmt is referenced only on debug paths; keep the import live with a
// trivial sink so the linter doesn't complain when those paths aren't hit.
var _ = fmt.Sprintf
