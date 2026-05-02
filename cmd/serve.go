package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/johnbuluba/dockersnap/internal/api"
	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/internal/dockerd"
	"github.com/johnbuluba/dockersnap/internal/instance"
	"github.com/johnbuluba/dockersnap/internal/plugin"
	"github.com/johnbuluba/dockersnap/internal/proxy"
)

var serveCmd = &cobra.Command{
	Use:     "serve",
	GroupID: groupAdmin,
	Short:   "Start the dockersnap API daemon",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: cfg.SlogLevel(),
		}))
		slog.SetDefault(logger)

		logger.Info("log level configured", "level", cfg.SlogLevel().String())

		mgr, err := instance.NewManager(cfg)
		if err != nil {
			return fmt.Errorf("creating instance manager: %w", err)
		}

		// Ensure systemd-networkd won't manage our veth interfaces.
		// Must happen before any instances are created/reconciled.
		dockerd.EnsureNetworkdIgnoresVeths(logger)

		// Ensure kernel limits are high enough for parallel kind clusters.
		// Each kind node runs systemd which needs inotify instances.
		ensureKernelLimits(logger)

		// Discover and load workload plugins. Failures (missing dir, broken
		// schema, init exit-1) are logged but don't fail startup — instances
		// without bound workloads keep working.
		pluginMgr := plugin.NewManager(cfg.Plugins.Dir,
			pluginTimeoutsFromConfig(cfg.Plugins.Timeouts), logger)
		if err := pluginMgr.Discover(cmd.Context()); err != nil {
			logger.Error("plugin discovery failed", "error", err)
			// Non-fatal: instances without workloads still work.
		}
		mgr.SetPlugins(pluginMgr)

		// Reconcile state on startup: restart any instances that should be running
		if err := mgr.Reconcile(cmd.Context()); err != nil {
			logger.Error("reconciliation failed", "error", err)
			// Non-fatal: continue serving even if some instances failed to start
		}

		srv := api.NewServer(cfg, mgr, logger)
		srv.SetPluginAdmin(pluginMgr)

		// Graceful shutdown on signals
		ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		// Start background port watcher for auto-discovery
		// Primary: event-driven (instant, uses docker events)
		eventListener := proxy.NewEventListener(mgr.Proxy(), mgr, logger)
		go eventListener.Run(ctx)

		// Fallback: slow polling (30s) catches anything events might miss
		watcher := proxy.NewWatcher(mgr.Proxy(), mgr, 30*time.Second, logger)
		go watcher.Run(ctx)

		// Periodic workload health poller. Default 30s; configurable via
		// plugins.health_poll_interval (set to "0s" to disable).
		if interval := healthPollInterval(cfg); interval > 0 {
			poller := instance.NewHealthPoller(mgr, interval,
				cfg.Plugins.HealthFailureThreshold, logger)
			srv.SetHealthReader(poller.Cache())
			go poller.Run(ctx)
		}

		slog.Info("starting dockersnap API server", "listen", cfg.API.Listen)
		return srv.Run(ctx)
	},
}

// pluginTimeoutsFromConfig parses durations from the YAML config, falling
// back to internal defaults for unset / invalid entries.
func pluginTimeoutsFromConfig(t config.PluginTimeouts) plugin.Timeouts {
	parse := func(s string, fallback time.Duration) time.Duration {
		if s == "" {
			return fallback
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return fallback
		}
		return d
	}
	def := plugin.DefaultTimeouts()
	return plugin.Timeouts{
		Init:     parse(t.Init, def.Init),
		Schema:   parse(t.Schema, def.Schema),
		Validate: parse(t.Validate, def.Validate),
		Deploy:   parse(t.Deploy, def.Deploy),
		Teardown: parse(t.Teardown, def.Teardown),
		Access:   parse(t.Access, def.Access),
		Describe: parse(t.Describe, def.Describe),
		Health:   parse(t.Health, def.Health),
	}
}

func healthPollInterval(cfg *config.Config) time.Duration {
	if cfg.Plugins.HealthPollInterval == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(cfg.Plugins.HealthPollInterval)
	if err != nil {
		return 30 * time.Second
	}
	return d
}

func init() {
	serveCmd.Flags().StringVar(&cfgFile, "config", "/etc/dockersnap/config.yaml", "config file path")
	rootCmd.AddCommand(serveCmd)
}

// ensureKernelLimits sets kernel parameters needed for running multiple kind
// clusters in parallel. Each kind node runs systemd which requires inotify
// instances and watches. Without raising these, clones fail with
// "Failed to create control group inotify object: Too many open files".
func ensureKernelLimits(logger *slog.Logger) {
	limits := map[string]string{
		"fs.inotify.max_user_instances": "1024",
		"fs.inotify.max_user_watches":   "524288",
	}
	for key, val := range limits {
		if err := exec.Command("sysctl", "-w", key+"="+val).Run(); err != nil {
			logger.Warn("failed to set kernel limit", "key", key, "value", val, "error", err)
		} else {
			logger.Debug("kernel limit set", "key", key, "value", val)
		}
	}
}
