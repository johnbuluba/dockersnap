package dockerd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonConfig is written as daemon.json for each instance's dockerd.
type DaemonConfig struct {
	DataRoot     string            `json:"data-root"`
	ExecRoot     string            `json:"exec-root"`
	PidFile      string            `json:"pidfile"`
	Hosts        []string          `json:"hosts"`
	Storage      string            `json:"storage-driver"`
	Bridge       string            `json:"bridge,omitempty"`
	CgroupParent string            `json:"cgroup-parent,omitempty"`
	ExecOpts     []string          `json:"exec-opts,omitempty"`
	DNS          []string          `json:"dns,omitempty"`
	LogDriver    string            `json:"log-driver"`
	LogOpts      map[string]string `json:"log-opts,omitempty"`
	IPTables     bool              `json:"iptables"`
	IPMasq       bool              `json:"ip-masq"`
	LiveRestore  bool              `json:"live-restore"`
	Experimental bool              `json:"experimental"`
}

// Manager manages dockerd lifecycle for instances.
type Manager struct {
	runDir        string
	configDir     string
	logger        *slog.Logger
	proxy         ProxyConfig
	hostInterface string
}

// ProxyConfig holds HTTP proxy settings for dockerd processes.
type ProxyConfig struct {
	HTTP    string
	HTTPS   string
	NoProxy string
}

// NewManager creates a new dockerd manager.
func NewManager(runDir string, logger *slog.Logger, proxy ProxyConfig, hostInterface string) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		runDir:        runDir,
		configDir:     "/var/lib/dockersnap/daemon-configs",
		logger:        logger,
		proxy:         proxy,
		hostInterface: hostInterface,
	}
}

// unitName returns the systemd transient unit name for an instance.
func unitName(name string) string {
	return fmt.Sprintf("dockersnap-%s", name)
}

// socketPath returns the docker socket path for an instance.
// Mirrors config.Config.SocketPath but local to this package so the dockerd
// manager doesn't need to import config.
func (m *Manager) socketPath(name string) string {
	return filepath.Join(m.runDir, name+".sock")
}

// configPath returns the daemon.json path for an instance.
func (m *Manager) configPath(name string) string {
	return filepath.Join(m.configDir, name+".json")
}

// RemoveConfig deletes the per-instance daemon.json. Called on Delete (not
// Stop — Snapshot/Revert call Stop and we still need the config for the
// next Start). Idempotent: missing file is not an error.
func (m *Manager) RemoveConfig(name string) error {
	path := m.configPath(name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing daemon config %s: %w", path, err)
	}
	return nil
}

// Start starts a dockerd for the given instance inside a systemd transient unit
// within an isolated network namespace.
func (m *Manager) Start(ctx context.Context, name, dataRoot, socket, pidFile, subnet string, dns []string) error {
	log := m.logger.With("instance", name)

	if err := os.MkdirAll(m.runDir, 0755); err != nil {
		return fmt.Errorf("creating run dir: %w", err)
	}
	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}

	execRoot := filepath.Join(m.runDir, "exec", name)
	if err := os.MkdirAll(execRoot, 0755); err != nil {
		return fmt.Errorf("creating exec root: %w", err)
	}

	netnsCfg := NetnsConfig{
		InstanceName:  name,
		Subnet:        subnet,
		HostInterface: m.hostInterface,
		Logger:        log,
	}
	if err := SetupNetns(ctx, netnsCfg); err != nil {
		return fmt.Errorf("setting up network namespace: %w", err)
	}

	// DNS chain for containers inside the netns:
	//   1. Host veth IP — dnsmasq listens here and serves .internal records,
	//      forwarding everything else upstream.
	//   2. Operator-supplied DNS from cfg.Docker.DNS — additional resolvers
	//      (corporate DNS, custom forwarders) that should be tried before
	//      we fall back to whatever the host happens to use.
	//   3. Host's upstream resolvers (from systemd-resolved or /etc/resolv.conf),
	//      filtered to skip loopback stub resolvers — these are useless from
	//      inside a different network namespace.
	hostIP, _, _, _ := DeriveNetnsIPs(subnet)
	dnsServers := []string{hostIP}
	seen := map[string]bool{hostIP: true}
	for _, s := range dns {
		if s == "" || seen[s] {
			continue
		}
		dnsServers = append(dnsServers, s)
		seen[s] = true
	}
	for _, s := range resolveHostDNS() {
		if s == "" || seen[s] {
			continue
		}
		dnsServers = append(dnsServers, s)
		seen[s] = true
	}

	cgroupParent := fmt.Sprintf("/dockersnap-%s", name)
	daemonCfg := &DaemonConfig{
		DataRoot:     dataRoot,
		ExecRoot:     execRoot,
		PidFile:      pidFile,
		Hosts:        []string{"unix://" + socket},
		Storage:      "overlay2",
		Bridge:       "",
		CgroupParent: cgroupParent,
		ExecOpts:     []string{"native.cgroupdriver=cgroupfs"},
		DNS:          dnsServers,
		LogDriver:    "json-file",
		LogOpts:      map[string]string{"max-size": "50m", "max-file": "3"},
		IPTables:     true,
		IPMasq:       true,
		LiveRestore:  true,
		Experimental: false,
	}

	cfgPath := m.configPath(name)
	cfgData, err := json.MarshalIndent(daemonCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling daemon config: %w", err)
	}
	if err := os.WriteFile(cfgPath, cfgData, 0644); err != nil {
		return fmt.Errorf("writing daemon config: %w", err)
	}

	unit := unitName(name)
	nsPath := fmt.Sprintf("/run/netns/%s", NetnsName(name))
	args := []string{
		"systemd-run",
		"--unit=" + unit,
		"--property=KillMode=control-group",
		"--property=Delegate=yes",
		"--property=TimeoutStopSec=60",
		"--property=Type=simple",
		"--property=NetworkNamespacePath=" + nsPath,
		"--collect",
	}

	if m.proxy.HTTP != "" {
		args = append(args, "--setenv=HTTP_PROXY="+m.proxy.HTTP, "--setenv=http_proxy="+m.proxy.HTTP)
	}
	if m.proxy.HTTPS != "" {
		args = append(args, "--setenv=HTTPS_PROXY="+m.proxy.HTTPS, "--setenv=https_proxy="+m.proxy.HTTPS)
	}
	if m.proxy.NoProxy != "" {
		args = append(args, "--setenv=NO_PROXY="+m.proxy.NoProxy, "--setenv=no_proxy="+m.proxy.NoProxy)
	}

	args = append(args, "--", "dockerd", "--config-file", cfgPath)

	log.Info("starting dockerd via systemd", "unit", unit, "data-root", dataRoot, "socket", socket)
	log.Debug("systemd-run command", "args", args)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("starting dockerd via systemd-run: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}

	if err := m.waitForSocket(ctx, socket, 60*time.Second); err != nil {
		if stopOut, stopErr := exec.Command("systemctl", "stop", unit+".service").CombinedOutput(); stopErr != nil {
			log.Warn("failed to stop unit after socket-wait failure",
				"error", stopErr, "output", strings.TrimSpace(string(stopOut)))
		}
		return fmt.Errorf("waiting for dockerd to be ready: %w", err)
	}

	log.Info("dockerd started successfully", "unit", unit)
	return nil
}

// Stop stops the dockerd and ALL child processes for the given instance.
//
// We deliberately do NOT issue `docker stop` on individual containers before
// killing dockerd. Per the Docker restart-policy semantics, an explicit
// `docker stop` flags each container as user-stopped, which makes
// `restart: unless-stopped` (and `always`) skip restart on the next dockerd
// startup — breaking workload survival across `dockersnap stop` + `start`.
// Instead, we let systemd's SIGTERM travel through to dockerd, whose own
// graceful shutdown stops containers in the right state ("daemon stopped",
// not "user stopped"). dockerd's default shutdown-timeout is 15s; that's
// enough for the workloads we run today (kind, echo).
func (m *Manager) Stop(ctx context.Context, name, pidFile string) error {
	log := m.logger.With("instance", name)
	unit := unitName(name)

	nsName := NetnsName(name)
	m.forceUnmountNFS(ctx, nsName, log)

	socket := m.socketPath(name)

	log.Info("stopping systemd unit (dockerd will gracefully stop containers)", "unit", unit)
	stopCmd := exec.CommandContext(ctx, "systemctl", "stop", unit+".service")
	if output, err := stopCmd.CombinedOutput(); err != nil {
		log.Warn("systemctl stop failed, falling back to pid-based stop",
			"error", err, "output", strings.TrimSpace(string(output)))
		m.stopByPid(ctx, name, pidFile, log)
	}

	m.cleanupCgroup(ctx, name, log)

	execDir := filepath.Join(m.runDir, "exec", name)
	m.cleanupMounts(ctx, name, execDir, log)

	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		log.Warn("failed to remove socket", "socket", socket, "error", err)
	}

	if err := TeardownNetns(ctx, name, log); err != nil {
		log.Warn("netns teardown failed", "error", err)
	}

	log.Info("dockerd stopped", "unit", unit)
	return nil
}

func (m *Manager) stopByPid(ctx context.Context, name, pidFile string, log *slog.Logger) {
	pid, err := m.readPid(pidFile)
	if err != nil {
		log.Warn("could not read pid file", "error", err)
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	if err := proc.Signal(os.Kill); err != nil {
		if !strings.Contains(err.Error(), "no such process") {
			log.Warn("error killing dockerd", "pid", pid, "error", err)
		}
	}
	time.Sleep(2 * time.Second)
}

func (m *Manager) cleanupMounts(ctx context.Context, name, execDir string, log *slog.Logger) {
	nsDir := filepath.Join(execDir, "netns")
	entries, err := os.ReadDir(nsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		nsPath := filepath.Join(nsDir, entry.Name())
		log.Info("unmounting leftover netns", "path", nsPath)
		if out, err := exec.CommandContext(ctx, "umount", "-l", nsPath).CombinedOutput(); err != nil {
			log.Warn("umount failed", "path", nsPath, "error", err, "output", strings.TrimSpace(string(out)))
		}
	}

	if err := os.RemoveAll(execDir); err != nil {
		log.Warn("failed to remove exec dir", "path", execDir, "error", err)
	}
}

func (m *Manager) forceUnmountNFS(ctx context.Context, nsName string, log *slog.Logger) {
	nsPath := fmt.Sprintf("/run/netns/%s", nsName)
	if _, err := os.Stat(nsPath); err != nil {
		return
	}

	out, err := exec.CommandContext(ctx, "nsenter", "--net="+nsPath, "--mount",
		"sh", "-c", "grep -E '\\bnfs4?\\b' /proc/mounts | awk '{print $2}'").Output()
	if err != nil {
		out, err = exec.CommandContext(ctx, "sh", "-c",
			"grep -E '\\bnfs4?\\b' /proc/mounts | awk '{print $2}'").Output()
		if err != nil {
			return
		}
	}

	mounts := strings.Fields(strings.TrimSpace(string(out)))
	if len(mounts) == 0 {
		return
	}

	log.Info("force-unmounting NFS mounts", "count", len(mounts))
	for _, mnt := range mounts {
		log.Debug("unmounting NFS", "path", mnt)
		if out, err := exec.CommandContext(ctx, "umount", "-f", "-l", mnt).CombinedOutput(); err != nil {
			log.Debug("NFS umount failed", "path", mnt, "error", err, "output", strings.TrimSpace(string(out)))
		}
	}

	time.Sleep(500 * time.Millisecond)
}

func (m *Manager) cleanupCgroup(ctx context.Context, name string, log *slog.Logger) {
	cgroupParent := fmt.Sprintf("/sys/fs/cgroup/dockersnap-%s", name)

	if _, err := os.Stat(cgroupParent); err != nil {
		return
	}

	err := filepath.WalkDir(cgroupParent, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == "cgroup.procs" {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			pids := strings.Fields(strings.TrimSpace(string(data)))
			for _, pidStr := range pids {
				pid, err := strconv.Atoi(pidStr)
				if err != nil {
					continue
				}
				log.Debug("killing stale cgroup process", "pid", pid, "cgroup", filepath.Dir(path))
				if proc, _ := os.FindProcess(pid); proc != nil {
					if err := proc.Signal(os.Kill); err != nil && !strings.Contains(err.Error(), "no such process") {
						log.Debug("failed to kill stale cgroup process", "pid", pid, "error", err)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Debug("error walking cgroup tree", "error", err)
	}

	time.Sleep(500 * time.Millisecond)

	if out, err := exec.CommandContext(ctx, "sh", "-c",
		fmt.Sprintf("find %s -depth -type d -exec rmdir {} + 2>/dev/null", cgroupParent)).CombinedOutput(); err != nil {
		log.Debug("cgroup rmdir cleanup had errors (often expected)",
			"error", err, "output", strings.TrimSpace(string(out)))
	}

	log.Info("cleaned up stale cgroup", "path", cgroupParent)
}

// IsRunning checks if a dockerd is running for the given instance.
// Prefers the systemd unit status, falls back to PID file.
func (m *Manager) IsRunning(pidFile string) bool {
	base := filepath.Base(pidFile)
	name := strings.TrimSuffix(base, ".pid")
	unit := unitName(name)

	cmd := exec.Command("systemctl", "is-active", "--quiet", unit+".service")
	if err := cmd.Run(); err == nil {
		return true
	}

	pid, err := m.readPid(pidFile)
	if err != nil {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0 probes liveness without affecting the process.
	return proc.Signal(syscall.Signal(0)) == nil
}

// IsRunningBatch is like IsRunning but probes many instances in a single
// systemctl invocation, then falls back to per-pid checks for any that
// systemd doesn't know about (pre-migration instances).
func (m *Manager) IsRunningBatch(pidFiles []string) map[string]bool {
	result := make(map[string]bool, len(pidFiles))
	if len(pidFiles) == 0 {
		return result
	}

	// Map unit names back to their pidFile keys.
	type entry struct {
		pidFile string
		unit    string
	}
	entries := make([]entry, 0, len(pidFiles))
	units := make([]string, 0, len(pidFiles))
	for _, pidFile := range pidFiles {
		base := filepath.Base(pidFile)
		name := strings.TrimSuffix(base, ".pid")
		unit := unitName(name) + ".service"
		entries = append(entries, entry{pidFile: pidFile, unit: unit})
		units = append(units, unit)
	}

	args := append([]string{"is-active"}, units...)
	out, _ := exec.Command("systemctl", args...).Output()
	// systemctl returns one status per unit, one per line, even on error.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	for i, e := range entries {
		var status string
		if i < len(lines) {
			status = strings.TrimSpace(lines[i])
		}
		if status == "active" {
			result[e.pidFile] = true
			continue
		}
		// Fallback: PID liveness check for pre-migration instances.
		pid, err := m.readPid(e.pidFile)
		if err != nil {
			result[e.pidFile] = false
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			result[e.pidFile] = false
			continue
		}
		result[e.pidFile] = proc.Signal(syscall.Signal(0)) == nil
	}

	return result
}

func (m *Manager) readPid(pidFile string) (int, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, fmt.Errorf("reading pid file %s: %w", pidFile, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parsing pid from %s: %w", pidFile, err)
	}
	return pid, nil
}

func (m *Manager) waitForSocket(ctx context.Context, socket string, timeout time.Duration) error {
	m.logger.Debug("waiting for docker socket", "socket", socket, "timeout", timeout.String())
	deadline := time.Now().Add(timeout)
	attempts := 0
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		attempts++
		conn, err := net.DialTimeout("unix", socket, 1*time.Second)
		if err == nil {
			conn.Close()
			m.logger.Debug("docker socket ready", "socket", socket, "attempts", attempts)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for socket %s after %s (%d attempts)", socket, timeout, attempts)
}

// resolveHostDNS reads nameserver entries from the host's resolv.conf,
// preferring the systemd-resolved upstream file over the stub.
func resolveHostDNS() []string {
	paths := []string{
		"/run/systemd/resolve/resolv.conf",
		"/etc/resolv.conf",
	}

	for _, path := range paths {
		servers := parseResolvConf(path)
		var valid []string
		for _, s := range servers {
			if !strings.HasPrefix(s, "127.") {
				valid = append(valid, s)
			}
		}
		if len(valid) > 0 {
			return valid
		}
	}
	return nil
}

func parseResolvConf(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				servers = append(servers, fields[1])
			}
		}
	}
	return servers
}
