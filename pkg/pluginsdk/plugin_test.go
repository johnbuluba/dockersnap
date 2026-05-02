package pluginsdk_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// runner wires a Runner up to in-memory I/O for testing. It returns the
// runner along with the buffers and the captured exit code.
type runnerHarness struct {
	r        *pluginsdk.Runner
	stdin    *bytes.Buffer
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	exitCode int
}

func newRunner(t *testing.T, p pluginsdk.Plugin, args ...string) *runnerHarness {
	t.Helper()
	h := &runnerHarness{
		stdin:  &bytes.Buffer{},
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
	r := pluginsdk.New(p)
	pluginsdk.WireForTest(r, append([]string{p.Name}, args...), h.stdin, h.stdout, h.stderr,
		func(code int) { h.exitCode = code })
	h.r = r
	return h
}

func TestRun_Schema(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{
		Name:                      "kind",
		Version:                   "1.0.0",
		Description:               "Test plugin",
		SupportedContractVersions: []string{"1"},
		ConfigOptions: []pluginsdk.ConfigOption{
			{Name: "cluster_name", Type: pluginsdk.ConfigTypeString, Default: "{{ instance_name }}"},
			{Name: "wait_ready", Type: pluginsdk.ConfigTypeBool, Default: true},
		},
	}, "schema")

	h.r.Run()
	assert.Equal(t, 0, h.exitCode)

	var resp pluginsdk.SchemaResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.Equal(t, "1", resp.ContractVersion)
	assert.Equal(t, "kind", resp.PluginName)
	assert.Equal(t, "1.0.0", resp.PluginVersion)
	assert.Equal(t, []string{"1"}, resp.SupportedContractVersions)
	require.Len(t, resp.ConfigOptions, 2)
	assert.Equal(t, "cluster_name", resp.ConfigOptions[0].Name)
	assert.Equal(t, pluginsdk.ConfigTypeString, resp.ConfigOptions[0].Type)
}

func TestRun_Schema_AutoFillsContractVersion(t *testing.T) {
	// SupportedContractVersions left empty in the Plugin{} — runner fills it in.
	h := newRunner(t, pluginsdk.Plugin{Name: "x", Version: "1"}, "schema")
	h.r.Run()

	var resp pluginsdk.SchemaResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.Equal(t, []string{"1"}, resp.SupportedContractVersions)
}

func TestRun_Init_NoHandlerSucceeds(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "init")
	h.r.Run()
	assert.Equal(t, 0, h.exitCode)
}

func TestRun_Init_FailingHandler(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "init")
	h.r.OnInit(func(ctx context.Context) error {
		return errors.New("kind binary missing")
	})
	h.r.Run()
	assert.Equal(t, 1, h.exitCode)
	assert.Contains(t, h.stderr.String(), "kind binary missing")
}

func TestRun_Validate_Valid(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{
		Name: "x",
		ConfigOptions: []pluginsdk.ConfigOption{
			{Name: "cluster_name", Type: pluginsdk.ConfigTypeString},
		},
	}, "validate")
	h.r.OnValidate(func(ctx context.Context, in *pluginsdk.Context) ([]string, error) {
		return []string{"k8s version not pinned"}, nil
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	assert.Equal(t, 0, h.exitCode)

	var resp pluginsdk.ValidateResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.True(t, resp.Valid)
	assert.Equal(t, []string{"k8s version not pinned"}, resp.Warnings)
}

func TestRun_Validate_Invalid(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "validate")
	h.r.OnValidate(func(ctx context.Context, in *pluginsdk.Context) ([]string, error) {
		return nil, errors.New("kind_config: file does not exist")
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	assert.Equal(t, 1, h.exitCode)

	var resp pluginsdk.ValidateResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.False(t, resp.Valid)
	require.Len(t, resp.Errors, 1)
	assert.Contains(t, resp.Errors[0], "kind_config")
}

func TestRun_Deploy_StreamsProgress(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "deploy")
	h.r.OnDeploy(func(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
		p.Step("creating", "Creating cluster")
		p.Done("creating")
		return nil
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	assert.Equal(t, 0, h.exitCode)

	lines := strings.Split(strings.TrimSpace(h.stdout.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 3, "expected creating-running, creating-done, complete")

	var first pluginsdk.ProgressEvent
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	assert.Equal(t, "creating", first.Step)
	assert.Equal(t, "running", first.Status)

	var last pluginsdk.ProgressEvent
	require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &last))
	assert.Equal(t, "complete", last.Step)
}

func TestRun_Deploy_HandlerError(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "deploy")
	h.r.OnDeploy(func(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
		return p.Fail("creating", errors.New("docker daemon unreachable"))
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	assert.Equal(t, 1, h.exitCode)
	assert.Contains(t, h.stdout.String(), `"status":"error"`)
	assert.Contains(t, h.stdout.String(), "docker daemon unreachable")
}

func TestRun_Access(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "access")
	h.r.OnAccess(func(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.AccessResponse, error) {
		return &pluginsdk.AccessResponse{
			Env: map[string]string{
				"KUBECONFIG": pluginsdk.AccessDirToken + "/kubeconfig",
			},
			Files: []pluginsdk.File{
				pluginsdk.FileFromString("kubeconfig",
					"server: https://"+pluginsdk.HostToken+":"+pluginsdk.PortToken("kubernetes-api"), 0600),
			},
		}, nil
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	assert.Equal(t, 0, h.exitCode)

	var resp pluginsdk.AccessResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.Equal(t, "1", resp.ContractVersion)
	require.Len(t, resp.Files, 1)
	assert.Equal(t, "kubeconfig", resp.Files[0].Name)
	assert.Equal(t, "0600", resp.Files[0].Mode)
	assert.Contains(t, resp.Files[0].Content, "${HOST}")
	assert.Contains(t, resp.Files[0].Content, "${PORT:kubernetes-api}")
	assert.Equal(t, "${ACCESS_DIR}/kubeconfig", resp.Env["KUBECONFIG"])
}

func TestRun_Health_HealthyByDefault(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "health")
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	assert.Equal(t, 0, h.exitCode)

	var resp pluginsdk.HealthResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.True(t, resp.Healthy)
}

func TestRun_Health_HandlerReportsUnhealthy(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "health")
	h.r.OnHealth(func(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.HealthResponse, error) {
		return &pluginsdk.HealthResponse{
			Healthy: false,
			Checks:  []pluginsdk.HealthCheck{{Name: "api-server", OK: false, Message: "timeout"}},
		}, nil
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	// Exit 0 even for unhealthy bodies — the daemon needs the diagnostic
	// Checks. `healthy: false` is in the JSON, not in the exit code.
	assert.Equal(t, 0, h.exitCode)

	var resp pluginsdk.HealthResponse
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &resp))
	assert.False(t, resp.Healthy)
	require.Len(t, resp.Checks, 1)
	assert.Equal(t, "timeout", resp.Checks[0].Message)
}

func TestRun_Health_HandlerErrorExitsNonZero(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "health")
	h.r.OnHealth(func(ctx context.Context, in *pluginsdk.Context) (*pluginsdk.HealthResponse, error) {
		return nil, errors.New("plugin couldn't run probes")
	})
	h.stdin.WriteString(`{"contract_version":"1","instance_name":"test","instance":{"name":"test"}}`)

	h.r.Run()
	// Real plugin error → exit 1 so the daemon surfaces it as a fault.
	assert.Equal(t, 1, h.exitCode)
}

func TestRun_UnknownCommand(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"}, "wat")
	h.r.Run()
	assert.Equal(t, 1, h.exitCode)
	assert.Contains(t, h.stderr.String(), "unknown command")
}

func TestRun_NoArgs(t *testing.T) {
	h := newRunner(t, pluginsdk.Plugin{Name: "x"})
	h.r.Run()
	assert.Equal(t, 1, h.exitCode)
	assert.Contains(t, h.stderr.String(), "usage")
}
