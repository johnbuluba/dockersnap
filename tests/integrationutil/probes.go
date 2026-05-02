package integrationutil

import (
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// System-state probes used by integration tests to verify the daemon's
// real on-host effects. These run as root on the dockersnap VM. Returning
// bool (rather than failing the test) lets each call site decide whether
// the probe matters for its assertion.

// ZFSDatasetExists returns true if the named dataset exists.
func ZFSDatasetExists(t testing.TB, dataset string) bool {
	t.Helper()
	return exec.Command("zfs", "list", "-H", "-o", "name", dataset).Run() == nil
}

// ZFSSnapshotExists returns true if the named dataset@snapshot exists.
func ZFSSnapshotExists(t testing.TB, dataset, snap string) bool {
	t.Helper()
	return exec.Command("zfs", "list", "-H", "-t", "snapshot", "-o", "name", dataset+"@"+snap).Run() == nil
}

// NetnsExists returns true if /run/netns/<nsName> exists.
func NetnsExists(t testing.TB, nsName string) bool {
	t.Helper()
	_, err := os.Stat("/run/netns/" + nsName)
	return err == nil
}

// VethExists returns true if a network interface with the given name exists.
func VethExists(t testing.TB, vethName string) bool {
	t.Helper()
	return exec.Command("ip", "link", "show", vethName).Run() == nil
}

// SystemdUnitActive returns true iff systemctl is-active --quiet returns 0.
func SystemdUnitActive(t testing.TB, unitName string) bool {
	t.Helper()
	return exec.Command("systemctl", "is-active", "--quiet", unitName).Run() == nil
}

// IPTablesHasComment looks for a literal substring (typically a rule
// comment we stamped on at instance create) in iptables -L output.
func IPTablesHasComment(t testing.TB, table, chain, comment string) bool {
	t.Helper()
	out, err := exec.Command("iptables", "-t", table, "-L", chain, "-n").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), comment)
}

// ResolveConfExists returns true if /etc/netns/<nsName>/resolv.conf was created.
func ResolveConfExists(t testing.TB, nsName string) bool {
	t.Helper()
	_, err := os.Stat("/etc/netns/" + nsName + "/resolv.conf")
	return err == nil
}

// SocketExists returns true if a unix socket can be dialed at path.
func SocketExists(t testing.TB, path string) bool {
	t.Helper()
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// DockerPing returns true iff `docker -H unix://<socket> info` succeeds.
func DockerPing(t testing.TB, socket string) bool {
	t.Helper()
	return exec.Command("docker", "-H", "unix://"+socket, "info", "--format", "{{.ID}}").Run() == nil
}

// MountpointHasData returns true if the mount point has any entries.
func MountpointHasData(t testing.TB, mountpoint string) bool {
	t.Helper()
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		return false
	}
	return len(entries) > 0
}

// ContainerCount runs `docker ps -aq` and returns the line count, or -1
// on error.
func ContainerCount(t testing.TB, socket string) int {
	t.Helper()
	out, err := exec.Command("docker", "-H", "unix://"+socket, "ps", "-aq").Output()
	if err != nil {
		return -1
	}
	return len(strings.Fields(strings.TrimSpace(string(out))))
}
