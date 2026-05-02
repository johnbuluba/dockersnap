// Package proxy provides a daemon-managed TCP proxy that forwards ports from
// the host to instance network namespaces.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// isAddrInUse reports whether err is the kernel's "address already in use"
// from net.Listen. We unwrap to check syscall.EADDRINUSE because
// errors.Is on a string match is fragile across Go versions.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Fallback for wrapped error chains that don't preserve the syscall
	// errno — match the standard message that net.OpError surfaces.
	return strings.Contains(err.Error(), "address already in use")
}

// PortMapping describes a single forwarded port.
//
// HostPort is the port the proxy listens on (host-side). DialPort is the
// port the proxy dials inside the network namespace — typically the same
// as docker's published host port, which is where docker-proxy listens
// for the published mapping. InContainerPort is the actual port the
// container exposes (e.g. 6443 for kind, 5678 for http-echo) and is what
// plugin authors care about when matching forwarded ports to their config.
type PortMapping struct {
	HostPort        int    `json:"host_port"`
	ContainerPort   int    `json:"container_port"`              // = DialPort; preserved for backwards-compatible JSON
	InContainerPort int    `json:"in_container_port,omitempty"` // actual in-container port (e.g. 5678 for echo)
	Protocol        string `json:"protocol"`
	Description     string `json:"description"`
	// RequestedHostPort is what the caller (i.e. dockerd via the proxy
	// scan) asked us to listen on. When that port was already taken on
	// the host we fall back to a kernel-picked free port; HostPort then
	// reflects the actual binding while RequestedHostPort preserves the
	// original ask. Equal to HostPort in the common no-collision case;
	// surfaced separately so callers (dashboard, debug tools) can show
	// the discrepancy when present.
	RequestedHostPort int `json:"requested_host_port,omitempty"`
}

// InstancePorts holds the active port forwarding state for one instance.
type InstancePorts struct {
	Instance string        `json:"instance"`
	TargetIP string        `json:"target_ip"`
	Ports    []PortMapping `json:"ports"`
}

// listener is a single active port forwarder.
type listener struct {
	mapping  PortMapping
	listener net.Listener
	cancel   context.CancelFunc
}

// instanceState holds per-instance proxy state.
type instanceState struct {
	targetIP  string
	nsName    string
	listeners map[int]*listener
}

// Manager manages TCP port forwarding for all instances.
type Manager struct {
	bindAddr string
	logger   *slog.Logger

	mu        sync.RWMutex
	instances map[string]*instanceState
}

// NewManager creates a new proxy manager.
// bindAddr is the address to bind forwarding listeners on. An empty string
// defaults to 127.0.0.1 (loopback only) for safety; callers that want remote
// access must opt in by setting it explicitly to "0.0.0.0" in config.
func NewManager(bindAddr string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	return &Manager{
		bindAddr:  bindAddr,
		logger:    logger,
		instances: make(map[string]*instanceState),
	}
}

// Forward starts forwarding a port from the host to an instance's namespace IP.
//
// dialPort is the port the proxy dials inside the netns. In production
// (Docker-published containers) dialPort equals hostPort because that's
// where docker-proxy listens inside the netns. In tests dialPort can be
// any reachable port.
//
// The PortMapping recorded for this forward will have InContainerPort=0 —
// use ForwardWithMetadata when the caller knows the actual in-container
// port (e.g. 5678 for hashicorp/http-echo) and wants plugins to be able
// to identify the forward by it.
func (m *Manager) Forward(instanceName, targetIP string, hostPort, dialPort int, description, nsName string) (int, error) {
	return m.ForwardWithMetadata(instanceName, targetIP, hostPort, dialPort, 0, description, nsName)
}

// ForwardWithMetadata is Forward plus an in-container-port hint for plugin
// matching. inContainerPort is metadata only — the proxy dials dialPort,
// not inContainerPort.
func (m *Manager) ForwardWithMetadata(instanceName, targetIP string, hostPort, dialPort, inContainerPort int, description, nsName string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	is, ok := m.instances[instanceName]
	if !ok {
		is = &instanceState{
			targetIP:  targetIP,
			nsName:    nsName,
			listeners: make(map[int]*listener),
		}
		m.instances[instanceName] = is
	}
	if nsName != "" {
		is.nsName = nsName
	}

	if _, exists := is.listeners[hostPort]; exists && hostPort != 0 {
		return hostPort, nil
	}

	// Same forward already registered under a different actual port? This
	// happens when a previous Forward() call landed in the EADDRINUSE
	// fallback path: the listener got registered under the kernel-picked
	// port (e.g. 43353) but the periodic re-scan still requests the
	// docker-published port (e.g. 32768). Without this check we'd open
	// a second listener for the same dial target each scan tick.
	for actual, l := range is.listeners {
		if l.mapping.ContainerPort == dialPort {
			return actual, nil
		}
	}

	listenAddr := fmt.Sprintf("%s:%d", m.bindAddr, hostPort)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		// Two instances on the same host can independently advertise the
		// same docker-published port (each dockerd's ephemeral allocator
		// starts low and often hands out the same number). When the
		// requested host port is taken, fall back to letting the kernel
		// pick any free port — callers don't depend on the exact value;
		// they read it back from the response.
		if hostPort != 0 && isAddrInUse(err) {
			fallback, fbErr := net.Listen("tcp", fmt.Sprintf("%s:0", m.bindAddr))
			if fbErr != nil {
				return 0, fmt.Errorf("listening on %s: %w; fallback :0 also failed: %v",
					listenAddr, err, fbErr)
			}
			ln = fallback
			// User-explicit fixed ports (anything not in the dockerd
			// ephemeral range, conventionally 32768+) are likely a
			// deliberate choice; overriding them silently is surprising,
			// so we log WARN instead of INFO. Ephemeral collisions
			// (clones competing for the same dockerd-picked port) are
			// expected and stay at INFO.
			level := slog.LevelInfo
			if hostPort < 32768 {
				level = slog.LevelWarn
			}
			m.logger.LogAttrs(nil, level, "host port collision, picked a free port",
				slog.String("instance", instanceName),
				slog.Int("requested_host_port", hostPort),
				slog.Int("actual_host_port", ln.Addr().(*net.TCPAddr).Port),
			)
		} else {
			return 0, fmt.Errorf("listening on %s: %w", listenAddr, err)
		}
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	requestedHostPort := 0
	if hostPort != 0 && hostPort != actualPort {
		// Only record a "requested" port when the original ask survived
		// distinctly. hostPort=0 means the caller didn't care; equal to
		// actualPort means there was no fallback so there's nothing to
		// disclose.
		requestedHostPort = hostPort
	}
	l := &listener{
		mapping: PortMapping{
			HostPort:          actualPort,
			ContainerPort:     dialPort, // legacy field name — value is the dial target
			InContainerPort:   inContainerPort,
			Protocol:          "tcp",
			Description:       description,
			RequestedHostPort: requestedHostPort,
		},
		listener: ln,
		cancel:   cancel,
	}
	is.listeners[actualPort] = l

	go m.serve(ctx, instanceName, ln, targetIP, dialPort)

	m.logger.Info("port forward started",
		"instance", instanceName,
		"listen", fmt.Sprintf("%s:%d", m.bindAddr, actualPort),
		"target", fmt.Sprintf("%s:%d", targetIP, dialPort),
		"in_container_port", inContainerPort,
		"description", description,
	)

	return actualPort, nil
}

// StopInstance stops all port forwards for an instance.
func (m *Manager) StopInstance(instanceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	is, ok := m.instances[instanceName]
	if !ok {
		return
	}

	for port, l := range is.listeners {
		l.cancel()
		l.listener.Close()
		m.logger.Info("port forward stopped", "instance", instanceName, "port", port)
	}

	delete(m.instances, instanceName)
}

// StopPort stops a single port forward for an instance.
func (m *Manager) StopPort(instanceName string, hostPort int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	is, ok := m.instances[instanceName]
	if !ok {
		return
	}

	l, ok := is.listeners[hostPort]
	if !ok {
		return
	}

	l.cancel()
	l.listener.Close()
	delete(is.listeners, hostPort)

	m.logger.Info("port forward stopped", "instance", instanceName, "port", hostPort)
}

// ListPorts returns the active port mappings for an instance.
func (m *Manager) ListPorts(instanceName string) *InstancePorts {
	m.mu.RLock()
	defer m.mu.RUnlock()

	is, ok := m.instances[instanceName]
	if !ok {
		return &InstancePorts{Instance: instanceName}
	}

	ports := &InstancePorts{
		Instance: instanceName,
		TargetIP: is.targetIP,
	}
	for _, l := range is.listeners {
		ports.Ports = append(ports.Ports, l.mapping)
	}
	return ports
}

// ListAllPorts returns port mappings for all instances.
func (m *Manager) ListAllPorts() []*InstancePorts {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []*InstancePorts
	for name, is := range m.instances {
		ip := &InstancePorts{
			Instance: name,
			TargetIP: is.targetIP,
		}
		for _, l := range is.listeners {
			ip.Ports = append(ip.Ports, l.mapping)
		}
		all = append(all, ip)
	}
	return all
}

// StopAll stops all port forwards for all instances.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, is := range m.instances {
		for _, l := range is.listeners {
			l.cancel()
			l.listener.Close()
		}
		m.logger.Info("all port forwards stopped", "instance", name)
	}
	m.instances = make(map[string]*instanceState)
}

// serve accepts connections on the listener and proxies them to the target.
// Transient Accept errors trigger a backoff retry rather than killing the goroutine.
func (m *Manager) serve(ctx context.Context, instanceName string, ln net.Listener, targetIP string, targetPort int) {
	var backoff time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Detect "use of closed network connection" — listener was Closed deliberately.
			if errors.Is(err, net.ErrClosed) {
				return
			}

			// Transient errors (EMFILE, ENFILE, dropped SYN queue, etc.) — back off and retry.
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				m.logger.Warn("accept timeout, retrying", "instance", instanceName, "error", err)
				continue
			}

			if backoff == 0 {
				backoff = 5 * time.Millisecond
			} else if backoff < time.Second {
				backoff *= 2
			}
			m.logger.Warn("accept error, retrying after backoff",
				"instance", instanceName, "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			continue
		}
		backoff = 0

		go m.handleConn(ctx, conn, targetIP, targetPort, instanceName)
	}
}

// handleConn proxies a single TCP connection.
func (m *Manager) handleConn(ctx context.Context, src net.Conn, targetIP string, targetPort int, instanceName string) {
	defer src.Close()

	targetAddr := fmt.Sprintf("%s:%d", targetIP, targetPort)

	m.mu.RLock()
	is := m.instances[instanceName]
	m.mu.RUnlock()

	var nsName string
	if is != nil {
		nsName = is.nsName
	}

	var (
		dst net.Conn
		err error
	)
	if nsName != "" {
		dst, err = dialViaNamespace(ctx, nsName, targetPort, m.logger.With("instance", instanceName))
	} else {
		dialer := net.Dialer{Timeout: 5 * time.Second}
		dst, err = dialer.DialContext(ctx, "tcp", targetAddr)
	}

	if err != nil {
		m.logger.Debug("proxy dial failed",
			"instance", instanceName, "target", targetAddr, "nsName", nsName, "error", err)
		return
	}
	defer dst.Close()

	// Bidirectional copy with half-close semantics where possible.
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(dst, src)
		// Half-close: signal EOF to the destination so it can terminate cleanly.
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(src, dst)
		if cw, ok := src.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}

// dialViaNamespace connects to 127.0.0.1:<port> inside a network namespace
// by running nsenter+socat. Returns a net.Conn that supports deadlines.
func dialViaNamespace(ctx context.Context, nsName string, port int, log *slog.Logger) (net.Conn, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.CommandContext(ctx, "nsenter", "--net=/run/netns/"+nsName, "--",
		"socat", "-", fmt.Sprintf("TCP:127.0.0.1:%d", port))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		// Fallback: try a direct dial in case socat isn't installed.
		log.Debug("nsenter+socat start failed, trying direct dial", "error", err)
		dialer := net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, "tcp", addr)
	}

	c := &nsConn{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		nsName: nsName,
		port:   port,
		log:    log,
	}

	// Watch the process; if it exits early, log it for visibility.
	go func() {
		err := cmd.Wait()
		if err != nil && !c.isClosed() {
			log.Debug("nsenter+socat exited unexpectedly", "ns", nsName, "port", port, "error", err)
		}
	}()

	return c, nil
}

// nsConn wraps a nsenter+socat process as a net.Conn, supporting deadlines via
// pipe close on timeout.
type nsConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	nsName string
	port   int
	log    *slog.Logger

	closed atomic.Bool

	readDeadline  atomic.Pointer[time.Time]
	writeDeadline atomic.Pointer[time.Time]
}

func (c *nsConn) isClosed() bool { return c.closed.Load() }

func (c *nsConn) Read(b []byte) (int, error) {
	if d := c.readDeadline.Load(); d != nil && !d.IsZero() {
		if !time.Now().Before(*d) {
			return 0, &deadlineErr{op: "read"}
		}
	}
	return c.stdout.Read(b)
}

func (c *nsConn) Write(b []byte) (int, error) {
	if d := c.writeDeadline.Load(); d != nil && !d.IsZero() {
		if !time.Now().Before(*d) {
			return 0, &deadlineErr{op: "write"}
		}
	}
	return c.stdin.Write(b)
}

func (c *nsConn) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.stdin.Close()
	c.stdout.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return nil
}

// CloseWrite closes the write half so the namespace-side socket sees EOF.
func (c *nsConn) CloseWrite() error {
	return c.stdin.Close()
}

func (c *nsConn) LocalAddr() net.Addr {
	return &nsAddr{nsName: c.nsName, port: 0, side: "local"}
}
func (c *nsConn) RemoteAddr() net.Addr {
	return &nsAddr{nsName: c.nsName, port: c.port, side: "remote"}
}
func (c *nsConn) SetDeadline(t time.Time) error {
	c.readDeadline.Store(&t)
	c.writeDeadline.Store(&t)
	return nil
}
func (c *nsConn) SetReadDeadline(t time.Time) error  { c.readDeadline.Store(&t); return nil }
func (c *nsConn) SetWriteDeadline(t time.Time) error { c.writeDeadline.Store(&t); return nil }

// nsAddr is a net.Addr that identifies a namespace-internal endpoint.
type nsAddr struct {
	nsName string
	port   int
	side   string
}

func (a *nsAddr) Network() string { return "tcp" }
func (a *nsAddr) String() string {
	if a.port == 0 {
		return fmt.Sprintf("netns://%s", a.nsName)
	}
	return fmt.Sprintf("netns://%s/127.0.0.1:%d", a.nsName, a.port)
}

// deadlineErr satisfies net.Error with Timeout()=true so callers treat it as such.
type deadlineErr struct{ op string }

func (e *deadlineErr) Error() string   { return "nsConn: " + e.op + " deadline exceeded" }
func (e *deadlineErr) Timeout() bool   { return true }
func (e *deadlineErr) Temporary() bool { return true }
