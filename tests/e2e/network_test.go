//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkNamespaceIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("netns")
	cleanup(t, name)
	defer cleanup(t, name)

	inst, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	nsName := "ds-" + name

	out, err := exec.Command("ip", "netns", "exec", nsName, "ip", "addr", "show", "veth0").Output()
	require.NoError(t, err, "exec in netns %s", nsName)
	assert.Contains(t, string(out), "inet ", "veth0 should have an IPv4 address")

	var a, b int
	_, _ = fmt.Sscanf(inst.Subnet, "%d.%d.", &a, &b)
	expectedNsIP := fmt.Sprintf("%d.%d.0.2", a, b)
	assert.Contains(t, string(out), expectedNsIP, "veth0 should have IP %s", expectedNsIP)

	out, _ = exec.Command("ip", "netns", "exec", nsName, "ip", "link", "show", "lo").Output()
	assert.Contains(t, string(out), "UP", "loopback should be UP in netns")

	out, _ = exec.Command("ip", "netns", "exec", nsName, "ip", "route", "show", "default").Output()
	expectedGW := fmt.Sprintf("%d.%d.0.1", a, b)
	assert.Contains(t, string(out), expectedGW, "default route should point to gateway %s", expectedGW)

	err = exec.Command("ip", "netns", "exec", nsName, "ping", "-c", "1", "-W", "2", expectedGW).Run()
	assert.NoError(t, err, "should be able to ping gateway %s from namespace", expectedGW)

	t.Logf("✓ Netns %s: veth0=%s gw=%s connectivity=OK", nsName, expectedNsIP, expectedGW)
}

func TestDaemonConfigValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("cfg")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	cfgPath := fmt.Sprintf("/var/lib/dockersnap/daemon-configs/%s.json", name)
	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err, "reading daemon config")

	var daemonCfg map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &daemonCfg), "parsing daemon config")

	assert.Equal(t, "overlay2", daemonCfg["storage-driver"])
	assert.Equal(t, true, daemonCfg["live-restore"])
	assert.Equal(t, "/dockersnap-"+name, daemonCfg["cgroup-parent"])

	// DNS: first entry should be the host veth IP (*.0.1).
	dnsSlice, ok := daemonCfg["dns"].([]interface{})
	require.True(t, ok, "dns should be an array")
	require.NotEmpty(t, dnsSlice, "dns array should not be empty")
	firstDNS := fmt.Sprintf("%v", dnsSlice[0])
	assert.True(t, strings.HasSuffix(firstDNS, ".0.1"),
		"dns[0] should be the host veth IP (*.0.1), got %s", firstDNS)

	// exec-opts must include the cgroupfs driver.
	execOpts, _ := daemonCfg["exec-opts"].([]interface{})
	found := false
	for _, opt := range execOpts {
		if fmt.Sprintf("%v", opt) == "native.cgroupdriver=cgroupfs" {
			found = true
			break
		}
	}
	assert.True(t, found, "exec-opts should include native.cgroupdriver=cgroupfs")

	t.Logf("✓ Daemon config: cgroup=/dockersnap-%s dns=%v", name, dnsSlice)
}

func TestPortForwarding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	name := instName("ports")
	cleanup(t, name)
	defer cleanup(t, name)

	_, err := c.Create(ctx, name, nil, nil)
	require.NoError(t, err, "Create")

	ports, err := c.Ports(ctx, name)
	require.NoError(t, err, "Ports")
	t.Logf("ports after create: %d", len(ports.Ports))

	ports, err = c.RefreshPorts(ctx, name)
	require.NoError(t, err, "RefreshPorts")
	t.Logf("ports after refresh: %d", len(ports.Ports))

	for _, p := range ports.Ports {
		addr := fmt.Sprintf("127.0.0.1:%d", p.HostPort)
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			t.Logf("  port %d (%s): not connectable", p.HostPort, p.Description)
		} else {
			conn.Close()
			t.Logf("  port %d (%s): connectable ✓", p.HostPort, p.Description)
		}
	}
}
