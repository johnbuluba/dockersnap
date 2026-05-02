//go:build integration

package echo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// TestEcho_SurvivesStopStart is the regression test for the
// 2026-05-02 bug where `dockersnap stop` followed by `dockersnap start`
// left the echo container Exited because (a) dockerd's stop path was
// running `docker stop` on every container before killing dockerd,
// flagging them as user-stopped, and (b) the echo plugin had no
// `--restart` policy so even after the first fix it wouldn't survive
// daemon restart.
//
// The cycle that must work: create → stop → start, and the same
// container ID is back up afterwards, the HTTP endpoint responds, and
// workload health is green. We pin the container ID because a brand
// new container would mean the policy didn't kick in — instead the
// instance's deploy ran again or the container was recreated, both of
// which would mask the bug.
func TestEcho_SurvivesStopStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	name := instName("survive")
	cleanup(t, name)
	defer cleanup(t, name)

	t.Logf("creating echo instance %q", name)
	_, err := c.Create(ctx, name, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err)

	access := waitForEchoEndpoint(t, ctx, name, 30*time.Second)
	beforeURL := echoURL(t, access)
	beforeBody := httpGet(t, beforeURL, 10*time.Second)
	require.Contains(t, beforeBody, "hello from "+name,
		"echo should respond before the stop/start cycle")

	beforeID := echoContainerID(t, ctx, name)
	require.NotEmpty(t, beforeID, "echo container should have a stable ID before stop")

	t.Log("stopping instance")
	require.NoError(t, c.Stop(ctx, name))

	t.Log("starting instance")
	require.NoError(t, c.Start(ctx, name))

	// Give dockerd's restart-policy processing a moment after start.
	access = waitForEchoEndpoint(t, ctx, name, 60*time.Second)
	afterURL := echoURL(t, access)

	afterID := echoContainerID(t, ctx, name)
	assert.Equal(t, beforeID, afterID,
		"the same echo container should be running after stop/start (was %q, now %q) — a fresh ID means the restart-policy fix regressed",
		beforeID, afterID)

	got := httpGet(t, afterURL, 10*time.Second)
	assert.Contains(t, got, "hello from "+name,
		"echo should respond after stop/start with the same configured text")

	// Force a fresh health probe to confirm the daemon also sees it as
	// healthy (not just an HTTP-200 from a half-broken container).
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		health, err := c.WorkloadHealth(ctx, name, true)
		if err == nil {
			if h, _ := health["healthy"].(bool); h {
				return
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("workload never reported healthy after stop/start cycle")
}

// TestEcho_CloneIsIndependentlyAccessible covers the clone flow from
// the user's perspective: the source service is reachable, snapshot,
// clone, and now BOTH services answer on distinct host-side ports
// without interfering with each other.
//
// This guards two things in one test:
//   - Clone preserves the workload (the bug we fixed in
//     fix(clone): inherit workload binding — see commit ecbc30c).
//   - The proxy's port-randomization-on-clone hands the clone a new
//     host port so the two instances don't collide on :HOSTPORT.
func TestEcho_CloneIsIndependentlyAccessible(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	src := instName("a")
	clone := instName("a-clone")
	cleanup(t, src)
	cleanup(t, clone)
	defer cleanup(t, src)
	defer cleanup(t, clone)

	// 1. Create echo instance "a".
	t.Logf("creating echo source %q", src)
	_, err := c.Create(ctx, src, &client.WorkloadInline{Plugin: "echo"}, nil)
	require.NoError(t, err, "Create source")

	srcAccess := waitForEchoEndpoint(t, ctx, src, 30*time.Second)
	srcURL := echoURL(t, srcAccess)
	srcPort := portFromURL(t, srcURL)

	// Sanity: source is alive before we snapshot.
	require.Contains(t, httpGet(t, srcURL, 10*time.Second), "hello from "+src)

	// 2. Snapshot, then clone.
	t.Log("snapshotting source as 'golden'")
	require.NoError(t, c.Snapshot(ctx, src, "golden", nil), "Snapshot")

	t.Logf("cloning %s@golden → %s", src, clone)
	_, err = c.Clone(ctx, src, "golden", clone)
	require.NoError(t, err, "Clone")

	cloneAccess := waitForEchoEndpoint(t, ctx, clone, 60*time.Second)
	cloneURL := echoURL(t, cloneAccess)
	clonePort := portFromURL(t, cloneURL)

	// 3. Both reachable, on different host-side ports.
	assert.NotEqual(t, srcPort, clonePort,
		"clone must be published on a different host port than the source — "+
			"got both at :%d, which means port randomization didn't run",
		srcPort)

	srcBody := httpGet(t, srcURL, 10*time.Second)
	cloneBody := httpGet(t, cloneURL, 10*time.Second)

	// Both echo containers were spawned with the same instance_name
	// template? Actually no — the source's container was created with
	// "hello from {{ instance_name }}" → "hello from <src>" baked in
	// as a process arg before the snapshot. The clone copies the ZFS
	// dataset bit-for-bit, so the cloned container ALSO answers
	// "hello from <src>" — that's expected, not a bug. We assert both
	// match the source's text so a future refactor that changes that
	// behavior trips this test for review.
	assert.Equal(t, srcBody, cloneBody,
		"clone snapshots the running container, so its echo text matches the source's")
	assert.Contains(t, srcBody, "hello from "+src)
}

// echoURL returns the resolved URL of the "echo" endpoint from an
// access response, failing the test if it's not present.
func echoURL(t *testing.T, access *pluginsdk.AccessResponse) string {
	t.Helper()
	for _, ep := range access.Endpoints {
		if ep.Name == "echo" && ep.URL != "" {
			return ep.URL
		}
	}
	t.Fatalf("no echo endpoint with resolved URL in access response: %+v", access.Endpoints)
	return ""
}

// portFromURL pulls the host-side port out of an http://host:port URL.
func portFromURL(t *testing.T, url string) string {
	t.Helper()
	// Format: http://host:port  (echo never uses TLS).
	const prefix = "http://"
	require.True(t, strings.HasPrefix(url, prefix), "expected http URL, got %q", url)
	rest := url[len(prefix):]
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	idx := strings.LastIndexByte(rest, ':')
	require.GreaterOrEqual(t, idx, 0, "URL %q has no :port component", url)
	port := rest[idx+1:]
	require.NotEmpty(t, port, "URL %q has empty port component", url)
	return port
}

// echoContainerID asks the daemon's docker socket for the echo
// container's ID via the workload describe endpoint's container info.
// Falls back to a `dockersnap docker <name> -- ps` shell-out via the
// plugin's labels would be cleaner, but describe already exposes the
// container details and is one less dependency for the test.
//
// Echo's describe response shape (per plugins/echo/describe.go) puts
// the container ID under details.container_id.
func echoContainerID(t *testing.T, ctx context.Context, name string) string {
	t.Helper()
	desc, err := c.Workload(ctx, name)
	require.NoError(t, err, "workload describe")
	details, ok := desc["details"].(map[string]interface{})
	if !ok {
		return ""
	}
	id, _ := details["container_id"].(string)
	return id
}
