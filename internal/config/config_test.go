package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path.yaml")
	require.NoError(t, err)

	assert.Equal(t, "dockersnap", cfg.ZFS.Pool)
	assert.Equal(t, "instances", cfg.ZFS.DatasetPrefix)
	assert.Equal(t, "127.0.0.1:9847", cfg.API.Listen)
	assert.Equal(t, "127.0.0.1", cfg.API.ProxyBind)
	assert.Equal(t, 16, cfg.Network.SubnetSize)
}

func TestLoad_FromFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	content := `
zfs:
  pool: testpool
  dataset_prefix: test-instances
api:
  listen: "0.0.0.0:8080"
  token: "mytoken"
network:
  base_offset: "192.168.0.0"
  subnet_size: 24
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(content), 0644))

	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	assert.Equal(t, "testpool", cfg.ZFS.Pool)
	assert.Equal(t, "test-instances", cfg.ZFS.DatasetPrefix)
	assert.Equal(t, "0.0.0.0:8080", cfg.API.Listen)
	assert.Equal(t, "mytoken", cfg.API.Token)
	assert.Equal(t, "192.168.0.0", cfg.Network.BaseOffset)
	assert.Equal(t, 24, cfg.Network.SubnetSize)
}

func TestConfig_Paths(t *testing.T) {
	cfg := Defaults()

	assert.Equal(t, "dockersnap/instances/myinst", cfg.DatasetPath("myinst"))
	assert.Equal(t, "/dockersnap/instances/myinst", cfg.MountPoint("myinst"))
	assert.Equal(t, "/run/dockersnap/myinst.sock", cfg.SocketPath("myinst"))
	assert.Equal(t, "/run/dockersnap/myinst.pid", cfg.PidFilePath("myinst"))
}
