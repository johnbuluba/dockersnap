package instance

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/internal/plugin"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// fakePluginRunner satisfies PluginRunner for tests. Each command name maps
// to a recorded call; lifecycle handlers can be customized via the Set*
// methods.
type fakePluginRunner struct {
	mu     sync.Mutex
	schema pluginsdk.SchemaResponse

	validateErr  error
	validateResp pluginsdk.ValidateResponse
	deployErr    error
	deployEvents []pluginsdk.ProgressEvent
	teardownErr  error
	accessResp   *pluginsdk.AccessResponse
	healthResp   *pluginsdk.HealthResponse
	healthErr    error
	calls        []string // command names invoked, in order
}

func newFakePluginRunner() *fakePluginRunner {
	return &fakePluginRunner{
		schema: pluginsdk.SchemaResponse{
			ContractVersion:           "1",
			SupportedContractVersions: []string{"1"},
			PluginName:                "fake",
			PluginVersion:             "0.0.1",
			ConfigOptions: []pluginsdk.ConfigOption{
				{Name: "cluster_name", Type: pluginsdk.ConfigTypeString,
					Default: "{{ instance_name }}"},
			},
		},
		validateResp: pluginsdk.ValidateResponse{
			ContractVersion: "1",
			Valid:           true,
		},
	}
}

func (f *fakePluginRunner) Get(name string) (*plugin.Plugin, error) {
	if name == "" {
		return nil, errors.New("empty name")
	}
	return &plugin.Plugin{Name: name, Schema: f.schema, Status: plugin.StatusReady}, nil
}

func (f *fakePluginRunner) Run(ctx context.Context, name, command string, in *pluginsdk.PluginInput, out interface{}) error {
	f.mu.Lock()
	f.calls = append(f.calls, command)
	f.mu.Unlock()

	switch command {
	case "validate":
		if f.validateErr != nil {
			return f.validateErr
		}
		if dst, ok := out.(*pluginsdk.ValidateResponse); ok {
			*dst = f.validateResp
		}
	case "access":
		if f.accessResp != nil {
			if dst, ok := out.(*pluginsdk.AccessResponse); ok {
				*dst = *f.accessResp
			}
		}
	case "describe":
		if dst, ok := out.(*pluginsdk.DescribeResponse); ok {
			*dst = pluginsdk.DescribeResponse{ContractVersion: "1", WorkloadType: "fake"}
		}
	case "health":
		if f.healthErr != nil {
			return f.healthErr
		}
		if f.healthResp != nil {
			if dst, ok := out.(*pluginsdk.HealthResponse); ok {
				*dst = *f.healthResp
			}
		}
	}
	return nil
}

func (f *fakePluginRunner) RunStream(ctx context.Context, name, command string, in *pluginsdk.PluginInput, events func(pluginsdk.ProgressEvent)) error {
	f.mu.Lock()
	f.calls = append(f.calls, command)
	f.mu.Unlock()

	switch command {
	case "deploy":
		for _, ev := range f.deployEvents {
			if events != nil {
				events(ev)
			}
		}
		return f.deployErr
	case "teardown":
		return f.teardownErr
	}
	return nil
}

func (f *fakePluginRunner) callsRecorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// testManagerWithPlugin wires a fake plugin runner into a fresh test manager.
func testManagerWithPlugin(t *testing.T) (*Manager, *fakePluginRunner) {
	t.Helper()
	mgr, _, _ := testManager(t)
	pr := newFakePluginRunner()
	mgr.SetPlugins(pr)
	return mgr, pr
}

// fakeWorkloadOpts is the inline-spec equivalent of the old "fake-default"
// preset: bind the fake plugin with cluster_name templated.
func fakeWorkloadOpts() WorkloadOpts {
	return WorkloadOpts{
		Plugin: "fake",
		Config: map[string]interface{}{"cluster_name": "{{ instance_name }}"},
	}
}

func TestCreate_NoWorkload_DoesNotCallPlugin(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "plain", WorkloadOpts{}, nil)
	require.NoError(t, err)

	assert.Empty(t, pr.callsRecorded(), "no plugin commands should run when workload is unset")
}

func TestCreate_WithPlugin_RunsValidateAndDeploy(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.deployEvents = []pluginsdk.ProgressEvent{
		{Step: "creating", Status: "running"},
		{Step: "creating", Status: "done"},
	}
	ctx := context.Background()

	inst, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)
	assert.Equal(t, "fake", inst.WorkloadPlugin)
	// {{ instance_name }} resolved to "env1".
	assert.Equal(t, "env1", inst.WorkloadConfig["cluster_name"])

	calls := pr.callsRecorded()
	assert.Equal(t, []string{"validate", "deploy"}, calls,
		"Create with workload should call validate then deploy")
}

func TestCreate_ValidateFailureRollsBack(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.validateResp = pluginsdk.ValidateResponse{Valid: false, Errors: []string{"bad config"}}
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.Error(t, err)

	insts, _ := mgr.List(ctx)
	assert.Empty(t, insts, "Create should roll back state when plugin validate fails")
}

func TestCreate_DeployFailureCleansUp(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.deployErr = errors.New("kind create cluster failed")
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.Error(t, err)

	insts, _ := mgr.List(ctx)
	assert.Empty(t, insts, "deploy failure must roll back state")

	calls := pr.callsRecorded()
	// validate -> deploy -> teardown (cleanup)
	assert.Equal(t, []string{"validate", "deploy", "teardown"}, calls)
}

func TestCreate_InlineWorkload(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", WorkloadOpts{
		Plugin: "fake",
		Config: map[string]interface{}{"cluster_name": "explicit"},
	}, nil)
	require.NoError(t, err)

	calls := pr.callsRecorded()
	assert.Equal(t, []string{"validate", "deploy"}, calls)
}

// TestCreate_InlineWorkload_FillsSchemaDefaults proves that when the user
// uses an inline plugin spec and omits a config key, the schema's Default
// (e.g. "{{ instance_name }}") is folded in and template-resolved by core.
// This is the regression test for the inline-create UX bug where kind got
// "{{ instance_name }}" passed verbatim to `kind create cluster`.
func TestCreate_InlineWorkload_FillsSchemaDefaults(t *testing.T) {
	mgr, _ := testManagerWithPlugin(t)
	ctx := context.Background()

	inst, err := mgr.Create(ctx, "env7", WorkloadOpts{
		Plugin: "fake",
		// No cluster_name set — should pull "{{ instance_name }}" from the
		// plugin's Default and resolve it.
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "env7", inst.WorkloadConfig["cluster_name"],
		"inline create with no cluster_name should fall back to the schema default and resolve {{ instance_name }}")
}

func TestDelete_RunsTeardown(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	pr.calls = nil // reset; we only care about Delete's calls
	require.NoError(t, mgr.Delete(ctx, "env1", nil))

	calls := pr.callsRecorded()
	assert.Contains(t, calls, "teardown")
}

func TestCallAccess_NoWorkloadReturnsNil(t *testing.T) {
	mgr, _ := testManagerWithPlugin(t)
	ctx := context.Background()

	_, err := mgr.Create(ctx, "plain", WorkloadOpts{}, nil)
	require.NoError(t, err)

	inst, err := mgr.Get(ctx, "plain")
	require.NoError(t, err)
	resp, err := mgr.CallAccess(ctx, inst, "host")
	require.NoError(t, err)
	assert.Nil(t, resp, "no-workload instance should return nil access response")
}

func TestCallAccess_ResolvesTokens(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.accessResp = &pluginsdk.AccessResponse{
		ContractVersion: "1",
		Files: []pluginsdk.File{
			{Name: "kubeconfig",
				Content: "server: https://${HOST}:${PORT:k8s}",
				Mode:    "0600"},
		},
	}
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)

	inst, err := mgr.Get(ctx, "env1")
	require.NoError(t, err)

	// Inject a port forwarding so the token resolves. The proxy.Manager is
	// the real one in testManager — push an entry with the matching label.
	_, err = mgr.proxy.Forward(inst.Name, "127.0.0.1", 0, 6443, "k8s", "")
	require.NoError(t, err)
	defer mgr.proxy.StopInstance(inst.Name)

	resp, err := mgr.CallAccess(ctx, inst, "10.0.0.5")
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Files, 1)
	assert.Contains(t, resp.Files[0].Content, "https://10.0.0.5:")
	assert.NotContains(t, resp.Files[0].Content, "${HOST}")
	assert.NotContains(t, resp.Files[0].Content, "${PORT:k8s}")
}

func TestCallHealth_ReportsErrorAsUnhealthy(t *testing.T) {
	mgr, pr := testManagerWithPlugin(t)
	pr.healthErr = errors.New("api server timeout")
	ctx := context.Background()

	_, err := mgr.Create(ctx, "env1", fakeWorkloadOpts(), nil)
	require.NoError(t, err)
	inst, _ := mgr.Get(ctx, "env1")

	resp, err := mgr.CallHealth(ctx, inst)
	require.NoError(t, err, "CallHealth should not return error itself; it surfaces unhealthy")
	require.NotNil(t, resp)
	assert.False(t, resp.Healthy)
	require.Len(t, resp.Checks, 1)
	assert.Contains(t, resp.Checks[0].Message, "api server timeout")
}
