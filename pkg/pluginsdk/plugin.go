package pluginsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Plugin is the metadata an author passes to New() to declare their plugin.
type Plugin struct {
	// Name is the plugin's binary name, e.g. "kind". Must match the file in
	// /usr/local/lib/dockersnap/plugins/.
	Name string

	// Version is a free-form version string surfaced in `dockersnap plugin
	// list`. Plugins built from this repo typically pass version.String()
	// from pkg/version (a SemVer 2.0 string composed by the Taskfile from
	// git state); plugins built elsewhere can hardcode "1.2.3" or whatever
	// their release process uses.
	Version string

	// Description is a short human-readable summary, surfaced in `schema`.
	Description string

	// SupportedContractVersions is the list of contract versions this plugin
	// understands. Core picks the highest mutual version. Most plugins emit
	// just []string{"1"}.
	SupportedContractVersions []string

	// ConfigOptions declares the plugin's accepted configuration keys.
	// Core uses this for schema-driven type checking before invoking
	// validate/deploy/etc.
	ConfigOptions []ConfigOption
}

// Handler signatures.
type (
	InitHandler     func(ctx context.Context) error
	ValidateHandler func(ctx context.Context, in *Context) (warnings []string, err error)
	DeployHandler   func(ctx context.Context, in *Context, p *Progress) error
	TeardownHandler func(ctx context.Context, in *Context, p *Progress) error
	AccessHandler   func(ctx context.Context, in *Context) (*AccessResponse, error)
	DescribeHandler func(ctx context.Context, in *Context) (*DescribeResponse, error)
	HealthHandler   func(ctx context.Context, in *Context) (*HealthResponse, error)
)

// Runner dispatches subcommands to registered handlers. Construct one with
// New(), register handlers via On*, then call Run().
type Runner struct {
	plugin   Plugin
	handlers handlers

	// I/O hooks for testing. Production uses os.Args/os.Stdin/os.Stdout/os.Stderr.
	args   []string
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	exit   func(int)

	// logHandler is the NDJSON slog handler used for both Runner-internal
	// logging (e.g. fatal, plugin lifecycle) and Context.Logger. Constructed
	// in New(); rebound per-invocation with the instance/command before
	// handlers fire.
	logHandler *ndjsonLogHandler
}

type handlers struct {
	init     InitHandler
	validate ValidateHandler
	deploy   DeployHandler
	teardown TeardownHandler
	access   AccessHandler
	describe DescribeHandler
	health   HealthHandler
}

// New creates a Runner with the given plugin metadata. It performs no I/O;
// authors register handlers, then call Run().
func New(p Plugin) *Runner {
	return &Runner{
		plugin:     p,
		args:       os.Args,
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		exit:       os.Exit,
		logHandler: newPluginHandler(os.Stderr, p.Name, levelFromEnv()),
	}
}

// Handler registration.
func (r *Runner) OnInit(h InitHandler)         { r.handlers.init = h }
func (r *Runner) OnValidate(h ValidateHandler) { r.handlers.validate = h }
func (r *Runner) OnDeploy(h DeployHandler)     { r.handlers.deploy = h }
func (r *Runner) OnTeardown(h TeardownHandler) { r.handlers.teardown = h }
func (r *Runner) OnAccess(h AccessHandler)     { r.handlers.access = h }
func (r *Runner) OnDescribe(h DescribeHandler) { r.handlers.describe = h }
func (r *Runner) OnHealth(h HealthHandler)     { r.handlers.health = h }

// ExitCode constants — the contract reserves a few specific codes.
const (
	ExitSuccess        = 0
	ExitError          = 1
	ExitNotImplemented = 64 // reserved for future hooks (deferred from v3)
)

// Run parses os.Args (or the args set via SetArgs in tests), dispatches to
// the registered handler, and exits. Run does not return on the production
// path; in tests, the exit hook can capture the code instead of terminating.
func (r *Runner) Run() {
	ctx := context.Background()

	if len(r.args) < 2 {
		r.fatal("usage: %s <command>", r.plugin.Name)
		return
	}
	command := r.args[1]

	switch command {
	case "init":
		r.runInit(ctx)
	case "schema":
		r.runSchema()
	case "validate":
		r.runValidate(ctx)
	case "deploy":
		r.runDeploy(ctx)
	case "teardown":
		r.runTeardown(ctx)
	case "access":
		r.runAccess(ctx)
	case "describe":
		r.runDescribe(ctx)
	case "health":
		r.runHealth(ctx)
	default:
		r.fatal("unknown command: %s", command)
	}
}

func (r *Runner) runInit(ctx context.Context) {
	if r.handlers.init == nil {
		r.exit(ExitSuccess)
		return
	}
	if err := r.handlers.init(ctx); err != nil {
		r.fatal("init: %v", err)
		return
	}
	r.exit(ExitSuccess)
}

func (r *Runner) runSchema() {
	resp := SchemaResponse{
		ContractVersion:           ContractVersion,
		SupportedContractVersions: r.plugin.SupportedContractVersions,
		PluginName:                r.plugin.Name,
		PluginVersion:             r.plugin.Version,
		Description:               r.plugin.Description,
		ConfigOptions:             r.plugin.ConfigOptions,
	}
	if len(resp.SupportedContractVersions) == 0 {
		resp.SupportedContractVersions = []string{ContractVersion}
	}
	r.writeJSON(&resp)
	r.exit(ExitSuccess)
}

func (r *Runner) readContext(command string) (*Context, error) {
	var in PluginInput
	if err := json.NewDecoder(r.stdin).Decode(&in); err != nil {
		return nil, fmt.Errorf("reading PluginInput: %w", err)
	}
	logger := slog.New(r.logHandler.withInstance(in.InstanceName, command))
	return &Context{
		InstanceName:   in.InstanceName,
		Instance:       in.Instance,
		ForwardedPorts: in.ForwardedPorts,
		Env:            in.Env,
		Config:         newConfig(in.PluginConfig, r.plugin.ConfigOptions),
		Logger:         logger,
	}, nil
}

func (r *Runner) runValidate(ctx context.Context) {
	in, err := r.readContext("validate")
	if err != nil {
		r.fatal("%v", err)
		return
	}
	resp := ValidateResponse{ContractVersion: ContractVersion, Valid: true}
	if r.handlers.validate != nil {
		warnings, err := r.handlers.validate(ctx, in)
		resp.Warnings = warnings
		if err != nil {
			resp.Valid = false
			resp.Errors = []string{err.Error()}
			r.writeJSON(&resp)
			r.exit(ExitError)
			return
		}
	}
	r.writeJSON(&resp)
	r.exit(ExitSuccess)
}

func (r *Runner) runDeploy(ctx context.Context) {
	r.runStreaming(ctx, "deploy", r.handlers.deploy)
}

func (r *Runner) runTeardown(ctx context.Context) {
	r.runStreaming(ctx, "teardown", r.handlers.teardown)
}

// runStreaming handles deploy/teardown — the two commands that emit NDJSON
// progress and have no JSON response body other than the terminal "complete"
// event.
func (r *Runner) runStreaming(ctx context.Context, name string, h func(context.Context, *Context, *Progress) error) {
	in, err := r.readContext(name)
	if err != nil {
		r.fatal("%v", err)
		return
	}
	if h == nil {
		// No handler registered — emit a terminal complete and exit OK.
		// Plugins that don't override are no-ops.
		p := NewProgress(r.stdout)
		p.Complete("%s: no-op", name)
		r.exit(ExitSuccess)
		return
	}
	progress := NewProgress(r.stdout)
	if err := h(ctx, in, progress); err != nil {
		// Handler is responsible for emitting an "error" event before
		// returning err; we just exit non-zero.
		r.exit(ExitError)
		return
	}
	// Auto-emit complete if the handler didn't.
	progress.Complete("%s succeeded", name)
	r.exit(ExitSuccess)
}

func (r *Runner) runAccess(ctx context.Context) {
	if r.handlers.access == nil {
		r.fatal("access: no handler registered")
		return
	}
	in, err := r.readContext("access")
	if err != nil {
		r.fatal("%v", err)
		return
	}
	resp, err := r.handlers.access(ctx, in)
	if err != nil {
		r.fatal("access: %v", err)
		return
	}
	if resp == nil {
		resp = &AccessResponse{}
	}
	resp.ContractVersion = ContractVersion
	r.writeJSON(resp)
	r.exit(ExitSuccess)
}

func (r *Runner) runDescribe(ctx context.Context) {
	if r.handlers.describe == nil {
		r.fatal("describe: no handler registered")
		return
	}
	in, err := r.readContext("describe")
	if err != nil {
		r.fatal("%v", err)
		return
	}
	resp, err := r.handlers.describe(ctx, in)
	if err != nil {
		r.fatal("describe: %v", err)
		return
	}
	if resp == nil {
		resp = &DescribeResponse{}
	}
	resp.ContractVersion = ContractVersion
	r.writeJSON(resp)
	r.exit(ExitSuccess)
}

func (r *Runner) runHealth(ctx context.Context) {
	in, err := r.readContext("health")
	if err != nil {
		r.fatal("%v", err)
		return
	}
	resp := &HealthResponse{ContractVersion: ContractVersion, Healthy: true}
	if r.handlers.health != nil {
		got, err := r.handlers.health(ctx, in)
		if err != nil {
			// Real plugin failure — handler couldn't even invoke its probes.
			// Emit a minimal body and exit non-zero so the daemon surfaces
			// this as a plugin-level fault (distinct from "workload reports
			// unhealthy via healthy: false in body").
			resp.Healthy = false
			resp.Checks = []HealthCheck{{Name: "plugin", OK: false, Message: err.Error()}}
			r.writeJSON(resp)
			r.exit(ExitError)
			return
		}
		if got != nil {
			got.ContractVersion = ContractVersion
			resp = got
		}
	}
	// Exit 0 for any handler-returned response, even if Healthy=false.
	// The body's `healthy` field is the source of truth — exit non-zero is
	// reserved for "plugin couldn't run at all" so the daemon can parse the
	// diagnostic Checks regardless of workload health.
	r.writeJSON(resp)
	r.exit(ExitSuccess)
}

func (r *Runner) writeJSON(v interface{}) {
	enc := json.NewEncoder(r.stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (r *Runner) fatal(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	resp := ErrorResponse{ContractVersion: ContractVersion, Error: msg}
	enc := json.NewEncoder(r.stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(&resp)
	fmt.Fprintln(r.stderr, msg)
	r.exit(ExitError)
}

// fatalf is the package-level fatal used by helpers like MustGetenv that don't
// have a Runner reference. It writes to os.Stderr and calls os.Exit.
func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "pluginsdk: "+format+"\n", args...)
	os.Exit(ExitError)
}

// WireForTest replaces the Runner's I/O hooks with the given values so tests
// can drive the dispatcher without spawning a real binary or terminating the
// test process. Production code never calls this.
func WireForTest(r *Runner, args []string, stdin io.Reader, stdout, stderr io.Writer, exit func(int)) {
	r.args = args
	r.stdin = stdin
	r.stdout = stdout
	r.stderr = stderr
	r.exit = exit
}
