package plugin

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// writeFakePlugin drops a shell-script plugin into dir/name. The script
// dispatches on $1 and writes whatever the test asked for.
//
// The body string is the body of a `case "$1" in ... esac` block.
func writeFakePlugin(t *testing.T, dir, name string, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nset -e\ncase \"$1\" in\n" + body + "\nesac\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	mgr := NewManager(dir, DefaultTimeouts(), slog.Default())
	return mgr, dir
}

func TestManager_Discover_ReadyPlugin(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"0.0.1","config_options":[{"name":"cluster_name","type":"string"}]}
EOF
  ;;
init)
  exit 0
  ;;
*)
  echo "unknown command: $1" >&2
  exit 1
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	p, err := mgr.Get("kind")
	require.NoError(t, err)
	assert.Equal(t, StatusReady, p.Status)
	assert.Equal(t, "kind", p.Schema.PluginName)
	assert.Equal(t, "0.0.1", p.Schema.PluginVersion)
	require.Len(t, p.Schema.ConfigOptions, 1)
	assert.Equal(t, "cluster_name", p.Schema.ConfigOptions[0].Name)
	assert.NotEmpty(t, p.SchemaDigest)
	assert.NotEmpty(t, p.BinaryDigest)
}

func TestManager_Discover_DisabledOnSchemaFailure(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "broken", `schema)
  echo "not json"
  ;;
init)
  exit 0
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	_, err := mgr.Get("broken")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")

	all := mgr.List()
	require.Len(t, all, 1)
	assert.Equal(t, StatusDisabled, all[0].Status)
	assert.Contains(t, all[0].Error, "parsing schema response")
}

func TestManager_Discover_DisabledOnInitFailure(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "noinit", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"noinit","plugin_version":"0.0.1"}
EOF
  ;;
init)
  echo "kind not installed" >&2
  exit 1
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	_, err := mgr.Get("noinit")
	require.Error(t, err)

	all := mgr.List()
	require.Len(t, all, 1)
	assert.Equal(t, StatusDisabled, all[0].Status)
	assert.Contains(t, all[0].Error, "running init")
}

func TestManager_Discover_RejectsIncompatibleContract(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "future", `schema)
  cat <<'EOF'
{"contract_version":"99","supported_contract_versions":["99"],"plugin_name":"future","plugin_version":"0.0.1"}
EOF
  ;;
init) exit 0 ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	all := mgr.List()
	require.Len(t, all, 1)
	assert.Equal(t, StatusDisabled, all[0].Status)
	assert.Contains(t, all[0].Error, "contract versions")
}

func TestManager_Discover_SkipsNonExecutableFiles(t *testing.T) {
	mgr, dir := newTestManager(t)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"),
		[]byte("not a plugin"), 0o644))

	require.NoError(t, mgr.Discover(context.Background()))
	assert.Empty(t, mgr.List())
}

func TestManager_Discover_HandlesMissingDir(t *testing.T) {
	mgr := NewManager("/nonexistent/path/here", DefaultTimeouts(), slog.Default())

	require.NoError(t, mgr.Discover(context.Background()))
	assert.Empty(t, mgr.List())
}

func TestManager_Reload_RefreshesCache(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"v1"}
EOF
  ;;
init) exit 0 ;;`)

	require.NoError(t, mgr.Discover(context.Background()))
	p, err := mgr.Get("kind")
	require.NoError(t, err)
	assert.Equal(t, "v1", p.Schema.PluginVersion)

	// Replace the binary's reported version.
	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"v2"}
EOF
  ;;
init) exit 0 ;;`)

	require.NoError(t, mgr.Reload(context.Background()))
	p, err = mgr.Get("kind")
	require.NoError(t, err)
	assert.Equal(t, "v2", p.Schema.PluginVersion)
}

func TestManager_Run_JSONInOut(t *testing.T) {
	mgr, dir := newTestManager(t)

	// Plugin's "describe" echoes a fixed DescribeResponse.
	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"0.0.1"}
EOF
  ;;
init) exit 0 ;;
describe)
  cat <<'EOF'
{"contract_version":"1","workload_type":"kind","status":"ready",
 "ports":[{"label":"kubernetes-api","container_port":6443,"protocol":"tcp"}]}
EOF
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	in := &pluginsdk.PluginInput{
		ContractVersion: "1",
		Command:         "describe",
		InstanceName:    "test",
	}
	var resp pluginsdk.DescribeResponse
	require.NoError(t, mgr.Run(context.Background(), "kind", "describe", in, &resp))

	assert.Equal(t, "kind", resp.WorkloadType)
	assert.Equal(t, "ready", resp.Status)
	require.Len(t, resp.Ports, 1)
	assert.Equal(t, "kubernetes-api", resp.Ports[0].Label)
}

func TestManager_Run_NonZeroExitReturnsError(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"0.0.1"}
EOF
  ;;
init) exit 0 ;;
access)
  echo "kind cluster missing" >&2
  exit 1
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	in := &pluginsdk.PluginInput{InstanceName: "test"}
	var resp pluginsdk.AccessResponse
	err := mgr.Run(context.Background(), "kind", "access", in, &resp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kind cluster missing")
}

func TestManager_RunStream_DeliversNDJSONEvents(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"0.0.1"}
EOF
  ;;
init) exit 0 ;;
deploy)
  echo '{"step":"creating","status":"running","message":"go"}'
  echo '{"step":"creating","status":"done"}'
  echo '{"step":"complete","status":"done","message":"ok"}'
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	var got []pluginsdk.ProgressEvent
	in := &pluginsdk.PluginInput{InstanceName: "test"}
	require.NoError(t, mgr.RunStream(context.Background(), "kind", "deploy", in,
		func(ev pluginsdk.ProgressEvent) { got = append(got, ev) }))

	require.Len(t, got, 3)
	assert.Equal(t, "creating", got[0].Step)
	assert.Equal(t, "running", got[0].Status)
	assert.Equal(t, "complete", got[2].Step)
}

func TestManager_RunStream_NonZeroExit(t *testing.T) {
	mgr, dir := newTestManager(t)

	writeFakePlugin(t, dir, "kind", `schema)
  cat <<'EOF'
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"kind","plugin_version":"0.0.1"}
EOF
  ;;
init) exit 0 ;;
deploy)
  echo '{"step":"creating","status":"running"}'
  echo "boom" >&2
  exit 1
  ;;`)

	require.NoError(t, mgr.Discover(context.Background()))

	var got []pluginsdk.ProgressEvent
	in := &pluginsdk.PluginInput{InstanceName: "test"}
	err := mgr.RunStream(context.Background(), "kind", "deploy", in,
		func(ev pluginsdk.ProgressEvent) { got = append(got, ev) })
	require.Error(t, err)
	assert.Len(t, got, 1, "events emitted before exit should still be delivered")
	assert.Contains(t, err.Error(), "boom")
}

func TestManager_Get_UnknownPlugin(t *testing.T) {
	mgr, _ := newTestManager(t)
	require.NoError(t, mgr.Discover(context.Background()))

	_, err := mgr.Get("missing")
	assert.Error(t, err)
}

func TestManager_List_OrderedByName(t *testing.T) {
	mgr, dir := newTestManager(t)

	for _, name := range []string{"zebra", "alpha", "midline"} {
		writeFakePlugin(t, dir, name, `schema)
  cat <<EOF
{"contract_version":"1","supported_contract_versions":["1"],"plugin_name":"`+name+`","plugin_version":"0.0.1"}
EOF
  ;;
init) exit 0 ;;`)
	}

	require.NoError(t, mgr.Discover(context.Background()))

	names := []string{}
	for _, p := range mgr.List() {
		names = append(names, p.Name)
	}
	assert.Equal(t, []string{"alpha", "midline", "zebra"}, names)
}
