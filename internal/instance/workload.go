package instance

import (
	"context"
	"fmt"

	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/internal/dockerd"
	"github.com/johnbuluba/dockersnap/internal/plugin"
	"github.com/johnbuluba/dockersnap/internal/proxy"
	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// WorkloadOpts is what the API/CLI passes to Create when the user wants a
// workload bound to the new instance.
type WorkloadOpts struct {
	// Plugin is the name of the plugin to deploy. Empty means "no workload,
	// plain Docker environment".
	Plugin string

	// Config is the plugin-specific config map. Type-checked against the
	// plugin's schema, with {{ instance_name }} substituted.
	Config map[string]interface{}
}

// resolveWorkload turns a WorkloadOpts into a (pluginName, resolvedConfig)
// pair. Returns ("", nil, nil) for no-workload (Plugin unset).
//
// Resolved config has {{ instance_name }} substituted and is type-checked
// against the plugin's schema.
func (m *Manager) resolveWorkload(opts WorkloadOpts, instanceName string) (string, map[string]interface{}, error) {
	if opts.Plugin == "" {
		return "", nil, nil
	}
	return m.prepareWorkload(opts.Plugin, opts.Config, instanceName)
}

func (m *Manager) prepareWorkload(pluginName string, raw map[string]interface{}, instanceName string) (string, map[string]interface{}, error) {
	if m.plugins == nil {
		return "", nil, fmt.Errorf("no plugin runner configured; workload %q cannot be used", pluginName)
	}
	p, err := m.plugins.Get(pluginName)
	if err != nil {
		return "", nil, fmt.Errorf("plugin %q: %w", pluginName, err)
	}
	// Merge plugin-schema defaults under the user-supplied raw config so
	// template tokens in the defaults (e.g. cluster_name's "{{ instance_name }}")
	// get resolved by core just like preset/inline values do. User-supplied
	// keys take precedence.
	merged := make(map[string]interface{}, len(p.Schema.ConfigOptions)+len(raw))
	for _, opt := range p.Schema.ConfigOptions {
		if opt.Default == nil {
			continue
		}
		if _, set := raw[opt.Name]; set {
			continue
		}
		merged[opt.Name] = opt.Default
	}
	for k, v := range raw {
		merged[k] = v
	}
	resolved := plugin.ResolveConfig(merged, instanceName)
	if issues := plugin.ValidateConfig(p.Schema.ConfigOptions, resolved); len(issues) > 0 {
		return "", nil, fmt.Errorf("invalid plugin_config for %q: %v", pluginName, issues)
	}
	return pluginName, resolved, nil
}

// runValidate calls the plugin's `validate` command (if a plugin is bound).
// Errors abort the deploy; warnings are logged.
func (m *Manager) runValidate(ctx context.Context, inst *state.Instance, progress *ProgressReporter) error {
	if inst.WorkloadPlugin == "" || m.plugins == nil {
		return nil
	}
	in := m.buildPluginInput(inst, "validate")
	var resp pluginsdk.ValidateResponse
	progress.Emit("plugin_validate", "running", "Validating workload config")
	if err := m.plugins.Run(ctx, inst.WorkloadPlugin, "validate", in, &resp); err != nil {
		progress.Emit("plugin_validate", "error", err.Error())
		return fmt.Errorf("plugin validate: %w", err)
	}
	if !resp.Valid {
		progress.Emit("plugin_validate", "error", joined(resp.Errors))
		return fmt.Errorf("plugin validate: %v", resp.Errors)
	}
	for _, w := range resp.Warnings {
		m.logger.Warn("plugin validate warning", "instance", inst.Name, "message", w)
	}
	progress.Emit("plugin_validate", "done", "")
	return nil
}

// runDeploy calls the plugin's `deploy` command, forwarding NDJSON progress
// events to the caller's progress reporter.
func (m *Manager) runDeploy(ctx context.Context, inst *state.Instance, progress *ProgressReporter) error {
	if inst.WorkloadPlugin == "" || m.plugins == nil {
		return nil
	}
	in := m.buildPluginInput(inst, "deploy")
	progress.Emit("plugin_deploy", "running", fmt.Sprintf("Deploying %s workload", inst.WorkloadPlugin))
	if err := m.plugins.RunStream(ctx, inst.WorkloadPlugin, "deploy", in, func(ev pluginsdk.ProgressEvent) {
		// Forward each plugin event verbatim. The plugin itself emits a
		// terminal "complete" event; we pass it through.
		progress.Emit(ev.Step, ev.Status, ev.Message)
	}); err != nil {
		progress.Emit("plugin_deploy", "error", err.Error())
		return fmt.Errorf("plugin deploy: %w", err)
	}
	return nil
}

// runTeardown calls the plugin's `teardown` command. Best-effort: failures
// are logged but don't abort the wider Delete operation.
func (m *Manager) runTeardown(ctx context.Context, inst *state.Instance, progress *ProgressReporter) {
	if inst.WorkloadPlugin == "" || m.plugins == nil {
		return
	}
	in := m.buildPluginInput(inst, "teardown")
	progress.Emit("plugin_teardown", "running", fmt.Sprintf("Tearing down %s workload", inst.WorkloadPlugin))
	if err := m.plugins.RunStream(ctx, inst.WorkloadPlugin, "teardown", in, func(ev pluginsdk.ProgressEvent) {
		progress.Emit(ev.Step, ev.Status, ev.Message)
	}); err != nil {
		m.logger.Warn("plugin teardown failed; continuing with instance delete",
			"instance", inst.Name, "plugin", inst.WorkloadPlugin, "error", err)
		progress.Emit("plugin_teardown", "error", err.Error())
		return
	}
}

// CallAccess invokes the plugin's `access` command, resolving tokens against
// current port forwardings + the supplied host. Returns nil for no-workload
// instances.
func (m *Manager) CallAccess(ctx context.Context, inst *state.Instance, host string) (*pluginsdk.AccessResponse, error) {
	if inst.WorkloadPlugin == "" {
		return nil, nil
	}
	if m.plugins == nil {
		return nil, fmt.Errorf("plugin runner not available")
	}
	in := m.buildPluginInput(inst, "access")
	var resp pluginsdk.AccessResponse
	if err := m.plugins.Run(ctx, inst.WorkloadPlugin, "access", in, &resp); err != nil {
		return nil, fmt.Errorf("plugin access: %w", err)
	}
	return plugin.ResolveAccess(&resp, plugin.TokenContext{
		Host:  host,
		Ports: m.portsByLabel(inst.Name),
	}), nil
}

// CallDescribe invokes the plugin's `describe`. Returns nil for no-workload
// instances.
func (m *Manager) CallDescribe(ctx context.Context, inst *state.Instance) (*pluginsdk.DescribeResponse, error) {
	if inst.WorkloadPlugin == "" {
		return nil, nil
	}
	if m.plugins == nil {
		return nil, fmt.Errorf("plugin runner not available")
	}
	in := m.buildPluginInput(inst, "describe")
	var resp pluginsdk.DescribeResponse
	if err := m.plugins.Run(ctx, inst.WorkloadPlugin, "describe", in, &resp); err != nil {
		return nil, fmt.Errorf("plugin describe: %w", err)
	}
	return &resp, nil
}

// CallHealth invokes the plugin's `health`. Returns (nil, nil) for instances
// with no workload bound — caller should treat that as "n/a".
func (m *Manager) CallHealth(ctx context.Context, inst *state.Instance) (*pluginsdk.HealthResponse, error) {
	if inst.WorkloadPlugin == "" {
		return nil, nil
	}
	if m.plugins == nil {
		return nil, fmt.Errorf("plugin runner not available")
	}
	in := m.buildPluginInput(inst, "health")
	var resp pluginsdk.HealthResponse
	if err := m.plugins.Run(ctx, inst.WorkloadPlugin, "health", in, &resp); err != nil {
		// Health failure is ok-ish — surface as unhealthy with the error.
		return &pluginsdk.HealthResponse{
			ContractVersion: pluginsdk.ContractVersion,
			Healthy:         false,
			Checks: []pluginsdk.HealthCheck{
				{Name: "plugin", OK: false, Message: err.Error()},
			},
		}, nil
	}
	return &resp, nil
}

// buildPluginInput produces a PluginInput from a state.Instance, the current
// proxy port mapping, and the daemon's proxy environment.
func (m *Manager) buildPluginInput(inst *state.Instance, command string) *pluginsdk.PluginInput {
	hostIP, nsIP, _, _ := dockerd.DeriveNetnsIPs(inst.Subnet)
	return &pluginsdk.PluginInput{
		ContractVersion: pluginsdk.ContractVersion,
		Command:         command,
		InstanceName:    inst.Name,
		Instance: pluginsdk.Instance{
			Name:       inst.Name,
			Socket:     inst.Socket,
			Subnet:     inst.Subnet,
			DataRoot:   m.cfg.MountPoint(inst.Name),
			HostVethIP: hostIP,
			NsVethIP:   nsIP,
			NetnsName:  dockerd.NetnsName(inst.Name),
			MetalLBIP:  inst.MetalLBIP,
			CreatedAt:  inst.CreatedAt,
			CloneOf:    inst.CloneOf,
		},
		ForwardedPorts: m.forwardedPorts(inst.Name),
		Env:            m.daemonProxyEnv(),
		PluginConfig:   inst.WorkloadConfig,
	}
}

func (m *Manager) forwardedPorts(name string) []pluginsdk.ForwardedPort {
	if m.proxy == nil {
		return nil
	}
	ports := m.proxy.ListPorts(name)
	if ports == nil || len(ports.Ports) == 0 {
		return nil
	}
	out := make([]pluginsdk.ForwardedPort, 0, len(ports.Ports))
	for _, p := range ports.Ports {
		label := p.Description
		// Surface the actual in-container port to plugins (not the proxy's
		// internal dial-target port, which equals HostPort for docker-published
		// containers). Falls back to the dial-target field when the proxy
		// didn't capture an in-container port (legacy entries / kind's
		// hardcoded labels).
		containerPort := p.InContainerPort
		if containerPort == 0 {
			containerPort = p.ContainerPort
		}
		out = append(out, pluginsdk.ForwardedPort{
			Label:         label,
			ContainerPort: containerPort,
			HostPort:      p.HostPort,
			Protocol:      p.Protocol,
		})
	}
	return out
}

func (m *Manager) portsByLabel(name string) map[string]int {
	out := make(map[string]int)
	for _, p := range m.forwardedPorts(name) {
		if p.Label != "" {
			out[p.Label] = p.HostPort
		}
	}
	return out
}

func (m *Manager) daemonProxyEnv() map[string]string {
	out := map[string]string{}
	if m.cfg.Docker.Proxy.HTTP != "" {
		out["HTTP_PROXY"] = m.cfg.Docker.Proxy.HTTP
		out["http_proxy"] = m.cfg.Docker.Proxy.HTTP
	}
	if m.cfg.Docker.Proxy.HTTPS != "" {
		out["HTTPS_PROXY"] = m.cfg.Docker.Proxy.HTTPS
		out["https_proxy"] = m.cfg.Docker.Proxy.HTTPS
	}
	if m.cfg.Docker.Proxy.NoProxy != "" {
		out["NO_PROXY"] = m.cfg.Docker.Proxy.NoProxy
		out["no_proxy"] = m.cfg.Docker.Proxy.NoProxy
	}
	return out
}

func joined(xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	out := xs[0]
	for _, x := range xs[1:] {
		out += "; " + x
	}
	return out
}

// Compile-time guards — keep these unused imports honest if we ever
// trim the package set above.
var _ = config.Defaults
var _ = (*proxy.Manager)(nil)
