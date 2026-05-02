//go:build integration

package e2e

import (
	"testing"

	"github.com/johnbuluba/dockersnap/tests/integrationutil"
)

// Thin local aliases so existing tests in this suite can keep their
// lower-case probe calls. The exported implementations live in
// tests/integrationutil/probes.go and are shared with tests/plugins/.

func zfsDatasetExists(t *testing.T, dataset string) bool {
	return integrationutil.ZFSDatasetExists(t, dataset)
}
func zfsSnapshotExists(t *testing.T, dataset, snap string) bool {
	return integrationutil.ZFSSnapshotExists(t, dataset, snap)
}
func netnsExists(t *testing.T, nsName string) bool {
	return integrationutil.NetnsExists(t, nsName)
}
func systemdUnitActive(t *testing.T, unitName string) bool {
	return integrationutil.SystemdUnitActive(t, unitName)
}
func iptablesHasComment(t *testing.T, table, chain, comment string) bool {
	return integrationutil.IPTablesHasComment(t, table, chain, comment)
}
func resolveConfExists(t *testing.T, nsName string) bool {
	return integrationutil.ResolveConfExists(t, nsName)
}
func socketExists(t *testing.T, path string) bool {
	return integrationutil.SocketExists(t, path)
}
func dockerPing(t *testing.T, socket string) bool {
	return integrationutil.DockerPing(t, socket)
}
func mountpointHasData(t *testing.T, mountpoint string) bool {
	return integrationutil.MountpointHasData(t, mountpoint)
}
func containerCount(t *testing.T, socket string) int {
	return integrationutil.ContainerCount(t, socket)
}
