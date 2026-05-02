package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/internal/dockerd"
	"github.com/johnbuluba/dockersnap/internal/network"
	"github.com/johnbuluba/dockersnap/internal/plugin"
	"github.com/johnbuluba/dockersnap/internal/proxy"
	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/internal/zfs"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// ProgressReporter allows emitting progress events during long operations.
// All methods are safe to call on a nil receiver (no-op).
type ProgressReporter struct {
	ch chan<- ProgressEvent
}

// ProgressEvent represents a single progress step.
type ProgressEvent struct {
	Step    string `json:"step"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// NewProgressReporter creates a reporter that sends events to the given channel.
func NewProgressReporter(ch chan<- ProgressEvent) *ProgressReporter {
	return &ProgressReporter{ch: ch}
}

// Emit sends a progress event. Safe to call on nil receiver.
// Non-blocking: drops events if the consumer can't keep up, to avoid
// wedging manager operations on a stalled HTTP client.
func (p *ProgressReporter) Emit(step, status, message string) {
	if p == nil || p.ch == nil {
		return
	}
	select {
	case p.ch <- ProgressEvent{Step: step, Status: status, Message: message}:
	default:
	}
}

// RevertOpts configures revert behavior.
type RevertOpts struct {
	Force bool
}

// DockerdManager defines the interface for Docker daemon operations.
type DockerdManager interface {
	Start(ctx context.Context, name, dataRoot, socket, pidFile, subnet string, dns []string) error
	Stop(ctx context.Context, name, pidFile string) error
	IsRunning(pidFile string) bool
	IsRunningBatch(pidFiles []string) map[string]bool
	// RemoveConfig deletes the per-instance daemon.json file. Called by
	// Manager.Delete after dockerd has stopped — Snapshot/Revert use Stop
	// without removing the config since the next Start needs it.
	RemoveConfig(name string) error
}

// PluginRunner is the subset of internal/plugin.Manager that instance.Manager
// uses. Defined as an interface here so tests don't need to spin up real
// plugin binaries.
type PluginRunner interface {
	Get(name string) (*plugin.Plugin, error)
	Run(ctx context.Context, name, command string, in *pluginsdk.PluginInput, out interface{}) error
	RunStream(ctx context.Context, name, command string, in *pluginsdk.PluginInput, events func(pluginsdk.ProgressEvent)) error
}

// Manager orchestrates instance lifecycle operations.
type Manager struct {
	cfg       *config.Config
	state     *state.Store
	zfs       zfs.Commander
	dockerd   DockerdManager
	allocator *network.Allocator
	proxy     *proxy.Manager
	plugins   PluginRunner // may be nil — instance ops then skip plugin invocations
	logger    *slog.Logger
}

// ManagerOpts allows injecting dependencies for testing.
type ManagerOpts struct {
	ZFS     zfs.Commander
	Dockerd DockerdManager
	State   *state.Store
	Proxy   *proxy.Manager
	Plugins PluginRunner
	Logger  *slog.Logger
}

// NewManager creates a new instance manager with default dependencies.
func NewManager(cfg *config.Config) (*Manager, error) {
	return NewManagerWithOpts(cfg, ManagerOpts{})
}

// NewManagerWithOpts creates a new instance manager with optional dependency injection.
func NewManagerWithOpts(cfg *config.Config, opts ManagerOpts) (*Manager, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	alloc, err := network.NewAllocator(cfg.Network.BaseOffset, cfg.Network.SubnetSize)
	if err != nil {
		return nil, fmt.Errorf("creating network allocator: %w", err)
	}

	zfsCmd := opts.ZFS
	if zfsCmd == nil {
		zfsCmd = zfs.NewCLI(logger)
	}

	dockerdMgr := opts.Dockerd
	if dockerdMgr == nil {
		proxyConfig := dockerd.ProxyConfig{
			HTTP:    cfg.Docker.Proxy.HTTP,
			HTTPS:   cfg.Docker.Proxy.HTTPS,
			NoProxy: cfg.Docker.Proxy.NoProxy,
		}
		dockerdMgr = dockerd.NewManager(cfg.RunDir, logger, proxyConfig, cfg.Network.HostInterface)
	}

	proxyMgr := opts.Proxy
	if proxyMgr == nil {
		proxyMgr = proxy.NewManager(cfg.API.ProxyBind, logger)
	}

	stateStore := opts.State
	if stateStore == nil {
		stateStore = state.NewStore(cfg.StateFile)
	}

	return &Manager{
		cfg:       cfg,
		state:     stateStore,
		zfs:       zfsCmd,
		dockerd:   dockerdMgr,
		allocator: alloc,
		proxy:     proxyMgr,
		plugins:   opts.Plugins,
		logger:    logger,
	}, nil
}

// SetPlugins wires a plugin runner into an existing Manager. Used by
// cmd/serve.go after the runner is initialized at startup.
func (m *Manager) SetPlugins(pr PluginRunner) {
	m.plugins = pr
}

// Create creates a new instance: ZFS dataset, network allocation, dockerd
// startup, and (if a workload is bound) plugin validate + deploy.
func (m *Manager) Create(ctx context.Context, name string, opts WorkloadOpts, progress *ProgressReporter) (*state.Instance, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	log := m.logger.With("instance", name)

	// Resolve the workload binding before any I/O so config errors fail fast.
	pluginName, pluginConfig, err := m.resolveWorkload(opts, name)
	if err != nil {
		return nil, err
	}

	var (
		subnet, metallbIP string
		dataset           string
		mountPoint        string
		socket, pidFile   string
		subnetIdx         int
		dns               []string
	)

	// Phase 1: reserve the name and allocate the subnet under the state lock.
	if err := m.state.Update(func(st *state.State) error {
		if _, exists := st.Instances[name]; exists {
			return fmt.Errorf("instance %q already exists", name)
		}

		subnetIdx = st.NextFreeSubnetIndex()
		s, err := m.allocator.SubnetForIndexChecked(subnetIdx)
		if err != nil {
			return fmt.Errorf("allocating subnet: %w", err)
		}
		subnet = s
		ip, err := network.MetalLBIPForSubnet(subnet, m.cfg.Network.MetalLBHostOffset)
		if err != nil {
			return fmt.Errorf("calculating metallb IP: %w", err)
		}
		metallbIP = ip
		dataset = m.cfg.DatasetPath(name)
		mountPoint = m.cfg.MountPoint(name)
		socket = m.cfg.SocketPath(name)
		pidFile = m.cfg.PidFilePath(name)
		dns = m.cfg.Docker.DNS

		// Reserve immediately by writing a placeholder so concurrent Creates
		// don't pick the same subnet index. WorkloadPlugin / Config are
		// written here too — immutable for the instance lifetime.
		inst := &state.Instance{
			Name:           name,
			Dataset:        dataset,
			Subnet:         subnet,
			SubnetIndex:    subnetIdx,
			MetalLBIP:      metallbIP,
			Socket:         socket,
			PidFile:        pidFile,
			CreatedAt:      time.Now(),
			Status:         state.StatusError, // not yet ready
			Snapshots:      []state.SnapshotInfo{},
			WorkloadPlugin: pluginName,
			WorkloadConfig: pluginConfig,
		}
		if pluginName != "" {
			inst.WorkloadContractVersion = pluginsdk.ContractVersion
		}
		st.Instances[name] = inst
		if subnetIdx >= st.NextSubnetIndex {
			st.NextSubnetIndex = subnetIdx + 1
		}
		return nil
	}); err != nil {
		return nil, err
	}

	log.Debug("allocated network", "subnet_index", subnetIdx, "subnet", subnet, "metallb_ip", metallbIP)

	// Phase 2: heavy I/O outside the state lock. On failure, roll back the reservation.
	rollback := func() {
		if err := m.state.Update(func(st *state.State) error {
			delete(st.Instances, name)
			return nil
		}); err != nil {
			log.Error("failed to roll back state after Create failure", "error", err)
		}
	}

	// Plugin validate (before any ZFS work — fail-fast if config is wrong).
	if pluginName != "" {
		instSnap, _ := m.snapshotInstance(name)
		if instSnap != nil {
			if err := m.runValidate(ctx, instSnap, progress); err != nil {
				rollback()
				return nil, err
			}
		}
	}

	log.Info("creating ZFS dataset", "dataset", dataset)
	if err := m.zfs.CreateDataset(ctx, dataset); err != nil {
		rollback()
		return nil, fmt.Errorf("creating ZFS dataset: %w", err)
	}

	log.Info("starting dockerd", "data-root", mountPoint)
	if err := m.dockerd.Start(ctx, name, mountPoint, socket, pidFile, subnet, dns); err != nil {
		if destroyErr := m.zfs.DestroyDataset(ctx, dataset, true); destroyErr != nil {
			log.Warn("failed to destroy dataset after dockerd start failure", "error", destroyErr)
		}
		rollback()
		return nil, fmt.Errorf("starting dockerd: %w", err)
	}

	// Plugin deploy — runs on top of a fully-started dockerd.
	if pluginName != "" {
		instSnap, _ := m.snapshotInstance(name)
		if instSnap != nil {
			if err := m.runDeploy(ctx, instSnap, progress); err != nil {
				// Best-effort cleanup: teardown plugin → stop dockerd → destroy dataset → roll back state.
				m.runTeardown(ctx, instSnap, progress)
				if stopErr := m.dockerd.Stop(ctx, name, pidFile); stopErr != nil {
					log.Warn("failed to stop dockerd after deploy failure", "error", stopErr)
				}
				if destroyErr := m.zfs.DestroyDataset(ctx, dataset, true); destroyErr != nil {
					log.Warn("failed to destroy dataset after deploy failure", "error", destroyErr)
				}
				rollback()
				return nil, err
			}
		}
	}

	// Phase 3: mark as running.
	var inst *state.Instance
	if err := m.state.Update(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("instance %q disappeared from state during Create", name)
		}
		i.Status = state.StatusRunning
		inst = i
		return nil
	}); err != nil {
		return nil, fmt.Errorf("saving state: %w", err)
	}

	log.Info("instance created", "subnet", subnet, "metallb_ip", metallbIP)
	return inst, nil
}

// snapshotInstance returns a copy of the instance from current state, or nil
// if it doesn't exist. Used by Create to pass a stable snapshot to plugin
// hooks without holding the state lock during plugin execution.
func (m *Manager) snapshotInstance(name string) (*state.Instance, error) {
	var out *state.Instance
	err := m.state.View(func(st *state.State) error {
		if i, ok := st.Instances[name]; ok {
			cp := *i
			out = &cp
		}
		return nil
	})
	return out, err
}

// Get returns a single instance by name with up-to-date runtime status.
func (m *Manager) Get(ctx context.Context, name string) (*state.Instance, error) {
	var inst *state.Instance
	if err := m.state.View(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrNotFound, name)
		}
		copy := *i
		inst = &copy
		return nil
	}); err != nil {
		return nil, err
	}
	if m.dockerd.IsRunning(inst.PidFile) {
		inst.Status = state.StatusRunning
	} else {
		inst.Status = state.StatusStopped
	}
	return inst, nil
}

// Delete stops dockerd and destroys the ZFS dataset and all snapshots.
// If the instance has a workload plugin bound, its `teardown` runs before
// dockerd is stopped (best-effort — failure logs but doesn't abort).
func (m *Manager) Delete(ctx context.Context, name string, progress *ProgressReporter) error {
	log := m.logger.With("instance", name)

	var inst state.Instance
	if err := m.state.View(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrNotFound, name)
		}
		inst = *i
		return nil
	}); err != nil {
		return err
	}

	// Plugin teardown first — runs while dockerd is still up, giving the
	// plugin a chance to gracefully release the workload.
	m.runTeardown(ctx, &inst, progress)

	progress.Emit("stopping_dockerd", "running", "Stopping containers and dockerd...")
	m.proxy.StopInstance(name)
	if m.dockerd.IsRunning(inst.PidFile) {
		log.Info("stopping dockerd")
		if err := m.dockerd.Stop(ctx, name, inst.PidFile); err != nil {
			log.Warn("error stopping dockerd", "error", err)
		}
	} else {
		log.Debug("dockerd not running, skipping stop")
	}
	progress.Emit("stopping_dockerd", "done", "Dockerd stopped")

	progress.Emit("destroying_dataset", "running", fmt.Sprintf("Destroying ZFS dataset %s...", inst.Dataset))
	log.Info("destroying ZFS dataset", "dataset", inst.Dataset)
	if err := m.zfs.DestroyDataset(ctx, inst.Dataset, true); err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			log.Warn("dataset already absent, continuing cleanup", "dataset", inst.Dataset)
		} else {
			progress.Emit("destroying_dataset", "error", err.Error())
			return fmt.Errorf("destroying dataset: %w", err)
		}
	}
	progress.Emit("destroying_dataset", "done", "Dataset destroyed")

	// Remove the per-instance daemon.json. Best-effort — if it's already
	// gone (e.g. operator hand-cleaned), don't block delete.
	if err := m.dockerd.RemoveConfig(name); err != nil {
		log.Warn("failed to remove daemon config; continuing", "error", err)
	}

	if err := m.state.Update(func(st *state.State) error {
		delete(st.Instances, name)
		return nil
	}); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	progress.Emit("complete", "done", fmt.Sprintf("Instance %q deleted", name))
	log.Info("instance deleted")
	return nil
}

// Snapshot stops dockerd, takes a ZFS snapshot, and restarts dockerd.
func (m *Manager) Snapshot(ctx context.Context, name, label string, tags map[string]string, progress *ProgressReporter) error {
	return m.runStoppedZFSOp(ctx, stoppedZFSOp{
		name:     name,
		label:    label,
		opName:   "snapshot",
		zfsStep:  "zfs_snapshot",
		zfsVerb:  "Creating",
		dropPage: false,
		preCheck: func(i *state.Instance) error {
			if i.HasSnapshot(label) {
				return fmt.Errorf("snapshot %q already exists for instance %q", label, name)
			}
			return nil
		},
		zfsOp: func(dataset string) error {
			return m.zfs.Snapshot(ctx, dataset, label)
		},
		onSuccess: func(i *state.Instance) {
			i.Snapshots = append(i.Snapshots, state.SnapshotInfo{
				Label:     label,
				CreatedAt: time.Now(),
				Tags:      tags,
			})
		},
		successMsg: fmt.Sprintf("Snapshot %q created successfully", label),
	}, progress)
}

// Revert stops dockerd, rolls back the ZFS dataset, and restarts dockerd.
func (m *Manager) Revert(ctx context.Context, name, label string, opts RevertOpts, progress *ProgressReporter) error {
	return m.runStoppedZFSOp(ctx, stoppedZFSOp{
		name:     name,
		label:    label,
		opName:   "revert",
		zfsStep:  "zfs_rollback",
		zfsVerb:  "Rolling back to",
		dropPage: true,
		preCheck: func(i *state.Instance) error {
			if !i.HasSnapshot(label) {
				return fmt.Errorf("snapshot %q not found for instance %q", label, name)
			}
			return nil
		},
		zfsOp: func(dataset string) error {
			return m.zfs.Rollback(ctx, dataset, label)
		},
		onSuccess: func(i *state.Instance) {
			// Trim snapshots that came after the target — rollback -r destroys them.
			var remaining []state.SnapshotInfo
			for _, s := range i.Snapshots {
				remaining = append(remaining, s)
				if s.Label == label {
					break
				}
			}
			i.Snapshots = remaining
		},
		successMsg: fmt.Sprintf("Reverted to %q successfully", label),
	}, progress)
}

// stoppedZFSOp parameterizes Snapshot and Revert: both stop dockerd, sync,
// invoke a ZFS operation, optionally drop page caches, restart dockerd, and
// commit a state mutation.
type stoppedZFSOp struct {
	name       string
	label      string
	opName     string                      // "snapshot" / "revert" — appears in errors and logs
	zfsStep    string                      // progress step name for the ZFS op ("zfs_snapshot" / "zfs_rollback")
	zfsVerb    string                      // human-readable verb for progress message
	dropPage   bool                        // run `echo 3 > drop_caches` after the ZFS op
	preCheck   func(*state.Instance) error // run under the state View lock; abort the op if it returns err
	zfsOp      func(dataset string) error  // the actual ZFS call
	onSuccess  func(*state.Instance)       // mutate state under Update after success
	successMsg string
}

func (m *Manager) runStoppedZFSOp(ctx context.Context, op stoppedZFSOp, progress *ProgressReporter) error {
	log := m.logger.With("instance", op.name, "snapshot", op.label, "op", op.opName)

	var inst state.Instance
	if err := m.state.View(func(st *state.State) error {
		i, ok := st.Instances[op.name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrNotFound, op.name)
		}
		if op.preCheck != nil {
			if err := op.preCheck(i); err != nil {
				return err
			}
		}
		inst = *i
		return nil
	}); err != nil {
		return err
	}

	progress.Emit("stopping_dockerd", "running", "Stopping containers and dockerd...")
	log.Info("stopping dockerd")
	m.proxy.StopInstance(op.name)
	if err := m.dockerd.Stop(ctx, op.name, inst.PidFile); err != nil {
		return fmt.Errorf("stopping dockerd: %w", err)
	}
	progress.Emit("stopping_dockerd", "done", "Dockerd stopped")

	progress.Emit("sync", "running", "Syncing filesystem...")
	if err := exec.CommandContext(ctx, "sync").Run(); err != nil {
		log.Warn("sync command failed", "error", err)
	}
	time.Sleep(1 * time.Second)
	progress.Emit("sync", "done", "Filesystem synced")

	progress.Emit(op.zfsStep, "running", fmt.Sprintf("%s %q...", op.zfsVerb, op.label))
	log.Info("running ZFS op")
	if err := op.zfsOp(inst.Dataset); err != nil {
		// Try to restart dockerd even if the ZFS op fails.
		if restartErr := m.restartDockerd(ctx, &inst); restartErr != nil {
			log.Error("failed to restart dockerd after ZFS op failure", "error", restartErr)
		}
		progress.Emit(op.zfsStep, "error", err.Error())
		return fmt.Errorf("%s: %w", op.opName, err)
	}
	progress.Emit(op.zfsStep, "done", "ZFS op complete")

	if op.dropPage {
		// Drop kernel page cache to prevent stale mmap'd pages from corrupting
		// databases (bbolt/etcd) after rollback.
		progress.Emit("drop_caches", "running", "Dropping page cache...")
		log.Info("dropping page cache after rollback")
		if err := exec.CommandContext(ctx, "sh", "-c", "echo 3 > /proc/sys/vm/drop_caches").Run(); err != nil {
			log.Warn("drop_caches failed; revert may be unstable", "error", err)
		}
		progress.Emit("drop_caches", "done", "Page cache dropped")
	}

	progress.Emit("starting_dockerd", "running", "Restarting dockerd...")
	log.Info("restarting dockerd")
	if err := m.restartDockerd(ctx, &inst); err != nil {
		progress.Emit("starting_dockerd", "error", err.Error())
		return fmt.Errorf("restarting dockerd after %s: %w", op.opName, err)
	}
	progress.Emit("starting_dockerd", "done", "Dockerd started")

	if err := m.state.Update(func(st *state.State) error {
		i, ok := st.Instances[op.name]
		if !ok {
			return fmt.Errorf("instance %q disappeared during %s", op.name, op.opName)
		}
		if op.onSuccess != nil {
			op.onSuccess(i)
		}
		i.Status = state.StatusRunning
		return nil
	}); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	progress.Emit("complete", "done", op.successMsg)
	log.Info("op completed successfully")
	return nil
}

// Clone creates a new instance from a snapshot via ZFS clone.
func (m *Manager) Clone(ctx context.Context, sourceName, label, newName string, progress *ProgressReporter) (*state.Instance, error) {
	if err := ValidateName(newName); err != nil {
		return nil, err
	}
	log := m.logger.With("source", sourceName, "snapshot", label, "clone", newName)

	var (
		sourceDataset     string
		subnet, metallbIP string
		targetDataset     string
		mountPoint        string
		socket, pidFile   string
		subnetIdx         int
		dns               []string
	)

	// Phase 1: validate, allocate subnet, reserve the name.
	if err := m.state.Update(func(st *state.State) error {
		src, ok := st.Instances[sourceName]
		if !ok {
			return fmt.Errorf("source instance %q not found", sourceName)
		}
		if !src.HasSnapshot(label) {
			return fmt.Errorf("snapshot %q not found for instance %q", label, sourceName)
		}
		if _, exists := st.Instances[newName]; exists {
			return fmt.Errorf("instance %q already exists", newName)
		}

		subnetIdx = st.NextFreeSubnetIndex()
		s, err := m.allocator.SubnetForIndexChecked(subnetIdx)
		if err != nil {
			return fmt.Errorf("allocating subnet: %w", err)
		}
		subnet = s
		ip, err := network.MetalLBIPForSubnet(subnet, m.cfg.Network.MetalLBHostOffset)
		if err != nil {
			return fmt.Errorf("calculating metallb IP: %w", err)
		}
		metallbIP = ip
		sourceDataset = src.Dataset
		targetDataset = m.cfg.DatasetPath(newName)
		mountPoint = m.cfg.MountPoint(newName)
		socket = m.cfg.SocketPath(newName)
		pidFile = m.cfg.PidFilePath(newName)
		dns = m.cfg.Docker.DNS

		st.Instances[newName] = &state.Instance{
			Name:        newName,
			Dataset:     targetDataset,
			Subnet:      subnet,
			SubnetIndex: subnetIdx,
			MetalLBIP:   metallbIP,
			Socket:      socket,
			PidFile:     pidFile,
			CreatedAt:   time.Now(),
			Status:      state.StatusError,
			CloneOf:     fmt.Sprintf("%s@%s", sourceName, label),
			Snapshots:   []state.SnapshotInfo{},
			// Inherit the workload binding from the source — the cloned dataset
			// already has the workload running inside it, so the daemon needs
			// to know which plugin owns it for access/describe/health/teardown.
			WorkloadPlugin:          src.WorkloadPlugin,
			WorkloadConfig:          src.WorkloadConfig,
			WorkloadContractVersion: src.WorkloadContractVersion,
		}
		if subnetIdx >= st.NextSubnetIndex {
			st.NextSubnetIndex = subnetIdx + 1
		}
		return nil
	}); err != nil {
		return nil, err
	}

	progress.Emit("allocating_network", "done", fmt.Sprintf("Network allocated: %s", subnet))
	log.Debug("allocated clone network", "subnet_index", subnetIdx, "subnet", subnet, "metallb_ip", metallbIP)

	rollback := func() {
		if err := m.state.Update(func(st *state.State) error {
			delete(st.Instances, newName)
			return nil
		}); err != nil {
			log.Error("failed to roll back state after Clone failure", "error", err)
		}
	}

	// Phase 2: heavy I/O.
	snapshotPath := fmt.Sprintf("%s@%s", sourceDataset, label)

	progress.Emit("zfs_clone", "running", fmt.Sprintf("Cloning ZFS dataset from %s...", snapshotPath))
	log.Info("creating ZFS clone", "from", snapshotPath, "to", targetDataset)
	if err := m.zfs.Clone(ctx, snapshotPath, targetDataset); err != nil {
		progress.Emit("zfs_clone", "error", err.Error())
		rollback()
		return nil, fmt.Errorf("creating ZFS clone: %w", err)
	}
	progress.Emit("zfs_clone", "done", "ZFS clone created")

	// Pre-start patches: randomize host port bindings to avoid port conflicts with source.
	progress.Emit("patching_ports", "running", "Randomizing port bindings...")
	m.randomizeHostPorts(mountPoint, log)
	progress.Emit("patching_ports", "done", "Port bindings patched")

	progress.Emit("starting_dockerd", "running", "Starting dockerd for clone...")
	log.Info("starting dockerd for clone")
	if err := m.dockerd.Start(ctx, newName, mountPoint, socket, pidFile, subnet, dns); err != nil {
		if destroyErr := m.zfs.DestroyDataset(ctx, targetDataset, true); destroyErr != nil {
			log.Warn("failed to destroy clone dataset after dockerd start failure", "error", destroyErr)
		}
		progress.Emit("starting_dockerd", "error", err.Error())
		rollback()
		return nil, fmt.Errorf("starting dockerd for clone: %w", err)
	}
	progress.Emit("starting_dockerd", "done", "Dockerd started")

	progress.Emit("port_forwarding", "running", "Setting up port forwarding...")
	if _, nsIP, _, err := dockerd.DeriveNetnsIPs(subnet); err == nil && nsIP != "" {
		if err := m.proxy.ScanAndForward(ctx, newName, socket, nsIP); err != nil {
			log.Warn("clone port forwarding scan failed", "error", err)
		}
	}
	progress.Emit("port_forwarding", "done", "Port forwarding configured")

	var inst *state.Instance
	if err := m.state.Update(func(st *state.State) error {
		i, ok := st.Instances[newName]
		if !ok {
			return fmt.Errorf("instance %q disappeared from state during Clone", newName)
		}
		i.Status = state.StatusRunning
		inst = i
		return nil
	}); err != nil {
		return nil, fmt.Errorf("saving state: %w", err)
	}

	progress.Emit("complete", "done", fmt.Sprintf("Clone %q created successfully", newName))
	log.Info("clone created successfully", "subnet", subnet)
	return inst, nil
}

// Start starts the dockerd for a stopped instance.
func (m *Manager) Start(ctx context.Context, name string, progress *ProgressReporter) error {
	log := m.logger.With("instance", name)

	var inst state.Instance
	if err := m.state.View(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrNotFound, name)
		}
		inst = *i
		return nil
	}); err != nil {
		return err
	}

	if m.dockerd.IsRunning(inst.PidFile) {
		return fmt.Errorf("instance %q is already running", name)
	}

	progress.Emit("starting_dockerd", "running", "Starting dockerd...")
	log.Info("starting dockerd")
	if err := m.restartDockerd(ctx, &inst); err != nil {
		progress.Emit("starting_dockerd", "error", err.Error())
		return fmt.Errorf("starting dockerd: %w", err)
	}
	progress.Emit("starting_dockerd", "done", "Dockerd started")

	if err := m.state.Update(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("instance %q disappeared during Start", name)
		}
		i.Status = state.StatusRunning
		return nil
	}); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	progress.Emit("complete", "done", fmt.Sprintf("Instance %q started", name))
	log.Info("instance started")
	return nil
}

// Stop stops the dockerd for a running instance.
func (m *Manager) Stop(ctx context.Context, name string, progress *ProgressReporter) error {
	log := m.logger.With("instance", name)

	var inst state.Instance
	if err := m.state.View(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("%w: %q", ErrNotFound, name)
		}
		inst = *i
		return nil
	}); err != nil {
		return err
	}

	if !m.dockerd.IsRunning(inst.PidFile) {
		return fmt.Errorf("instance %q is not running", name)
	}

	progress.Emit("stopping_dockerd", "running", "Stopping containers and dockerd...")
	log.Info("stopping dockerd")
	m.proxy.StopInstance(name)
	if err := m.dockerd.Stop(ctx, name, inst.PidFile); err != nil {
		progress.Emit("stopping_dockerd", "error", err.Error())
		return fmt.Errorf("stopping dockerd: %w", err)
	}
	progress.Emit("stopping_dockerd", "done", "Dockerd stopped")

	if err := m.state.Update(func(st *state.State) error {
		i, ok := st.Instances[name]
		if !ok {
			return fmt.Errorf("instance %q disappeared during Stop", name)
		}
		i.Status = state.StatusStopped
		return nil
	}); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	progress.Emit("complete", "done", fmt.Sprintf("Instance %q stopped", name))
	log.Info("instance stopped")
	return nil
}

// List returns all instances with up-to-date runtime status.
// Uses a single batched probe to avoid one fork/exec per instance.
func (m *Manager) List(ctx context.Context) ([]*state.Instance, error) {
	var instances []*state.Instance
	if err := m.state.View(func(st *state.State) error {
		for _, i := range st.Instances {
			copy := *i
			instances = append(instances, &copy)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if len(instances) == 0 {
		return instances, nil
	}

	pidFiles := make([]string, len(instances))
	for i, inst := range instances {
		pidFiles[i] = inst.PidFile
	}
	running := m.dockerd.IsRunningBatch(pidFiles)
	for _, inst := range instances {
		if running[inst.PidFile] {
			inst.Status = state.StatusRunning
		} else {
			inst.Status = state.StatusStopped
		}
	}

	return instances, nil
}

// Proxy returns the proxy manager for port forwarding queries.
func (m *Manager) Proxy() *proxy.Manager {
	return m.proxy
}

// RunningInstances returns info about all running instances for port discovery.
// Implements proxy.InstanceLister.
func (m *Manager) RunningInstances() []proxy.InstanceInfo {
	instances, err := m.List(context.Background())
	if err != nil {
		return nil
	}

	var result []proxy.InstanceInfo
	for _, inst := range instances {
		if inst.Status != state.StatusRunning {
			continue
		}
		_, nsIP, _, err := dockerd.DeriveNetnsIPs(inst.Subnet)
		if err != nil {
			continue
		}
		result = append(result, proxy.InstanceInfo{
			Name:   inst.Name,
			Socket: inst.Socket,
			NsIP:   nsIP,
		})
	}
	return result
}

// SetupPortForwarding scans an instance's containers for published ports
// and starts TCP forwarding from the host to the instance's network namespace.
func (m *Manager) SetupPortForwarding(ctx context.Context, inst *state.Instance) {
	_, nsIP, _, err := dockerd.DeriveNetnsIPs(inst.Subnet)
	if err != nil {
		m.logger.Warn("could not derive netns IP for port forwarding", "instance", inst.Name, "error", err)
		return
	}

	if err := m.proxy.ScanAndForward(ctx, inst.Name, inst.Socket, nsIP); err != nil {
		m.logger.Warn("port scan/forward failed", "instance", inst.Name, "error", err)
	}
}

// restartDockerd restarts the dockerd for an instance and sets up port forwarding.
func (m *Manager) restartDockerd(ctx context.Context, inst *state.Instance) error {
	mountPoint := m.cfg.MountPoint(inst.Name)
	if err := m.dockerd.Start(ctx, inst.Name, mountPoint, inst.Socket, inst.PidFile, inst.Subnet, m.cfg.Docker.DNS); err != nil {
		return err
	}
	m.SetupPortForwarding(ctx, inst)
	return nil
}

// shortID truncates a container ID to 12 chars (Docker convention), tolerating
// any directory entry of any length.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// randomizeHostPorts modifies container hostconfig.json files on disk to clear
// HostPort values, which makes Docker assign random available ports when the
// container starts. This prevents port conflicts between source and clone.
//
// Note: rewriting the JSON loses original key ordering and re-formats numbers;
// debug `diff`s of containers will reflect this even when no semantic change occurred.
func (m *Manager) randomizeHostPorts(mountPoint string, log *slog.Logger) {
	containersDir := mountPoint + "/containers"
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		log.Warn("could not read containers dir for port patching", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hostconfigPath := containersDir + "/" + entry.Name() + "/hostconfig.json"
		data, err := os.ReadFile(hostconfigPath)
		if err != nil {
			continue
		}

		var hostconfig map[string]interface{}
		if err := json.Unmarshal(data, &hostconfig); err != nil {
			continue
		}

		portBindings, ok := hostconfig["PortBindings"]
		if !ok {
			continue
		}

		pb, ok := portBindings.(map[string]interface{})
		if !ok {
			continue
		}

		modified := false
		for port, bindings := range pb {
			bindList, ok := bindings.([]interface{})
			if !ok {
				continue
			}
			for i, b := range bindList {
				binding, ok := b.(map[string]interface{})
				if !ok {
					continue
				}
				if hp, exists := binding["HostPort"]; exists && hp != "" {
					binding["HostPort"] = ""
					bindList[i] = binding
					modified = true
				}
			}
			pb[port] = bindList
		}

		if modified {
			hostconfig["PortBindings"] = pb
			newData, err := json.Marshal(hostconfig)
			if err != nil {
				continue
			}
			if err := os.WriteFile(hostconfigPath, newData, 0644); err != nil {
				log.Warn("failed to write patched hostconfig", "container", shortID(entry.Name()), "error", err)
			} else {
				log.Info("randomized host port bindings", "container", shortID(entry.Name()))
			}
		}
	}
}
