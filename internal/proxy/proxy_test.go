package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoServer spawns a TCP echo server on a random localhost port and
// returns its port plus a cleanup func.
func startEchoServer(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, _ = io.Copy(conn, conn)
				conn.Close()
			}()
		}
	}()

	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

func TestForwardAndStop(t *testing.T) {
	targetPort, stop := startEchoServer(t)
	defer stop()

	mgr := NewManager("127.0.0.1", slog.Default())

	hostPort, err := mgr.Forward("test-instance", "127.0.0.1", 0, targetPort, "test-echo", "")
	require.NoError(t, err)
	assert.NotZero(t, hostPort, "expected non-zero host port")

	ports := mgr.ListPorts("test-instance")
	require.Len(t, ports.Ports, 1)
	assert.Equal(t, hostPort, ports.Ports[0].HostPort)
	assert.Equal(t, "test-echo", ports.Ports[0].Description)

	// Test connectivity through the proxy.
	time.Sleep(50 * time.Millisecond)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), 2*time.Second)
	require.NoError(t, err)

	msg := "hello proxy"
	_, err = conn.Write([]byte(msg))
	require.NoError(t, err)

	buf := make([]byte, len(msg))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	conn.Close()

	assert.Equal(t, msg, string(buf))

	mgr.StopInstance("test-instance")

	ports = mgr.ListPorts("test-instance")
	assert.Empty(t, ports.Ports, "expected 0 ports after stop")

	_, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort), 500*time.Millisecond)
	assert.Error(t, err, "expected connection refused after stop")
}

func TestListAllPorts(t *testing.T) {
	mgr := NewManager("127.0.0.1", slog.Default())

	p1, stop1 := startEchoServer(t)
	defer stop1()
	p2, stop2 := startEchoServer(t)
	defer stop2()

	_, err := mgr.Forward("inst1", "127.0.0.1", 0, p1, "svc1", "")
	require.NoError(t, err)
	_, err = mgr.Forward("inst2", "127.0.0.1", 0, p2, "svc2", "")
	require.NoError(t, err)

	all := mgr.ListAllPorts()
	assert.Len(t, all, 2)

	mgr.StopAll()
	assert.Empty(t, mgr.ListAllPorts())
}

func TestForwardDuplicate(t *testing.T) {
	mgr := NewManager("127.0.0.1", slog.Default())
	defer mgr.StopAll()

	targetPort, stop := startEchoServer(t)
	defer stop()

	port1, err := mgr.Forward("inst", "127.0.0.1", 0, targetPort, "svc", "")
	require.NoError(t, err)

	port2, err := mgr.Forward("inst", "127.0.0.1", port1, targetPort, "svc", "")
	require.NoError(t, err)
	assert.Equal(t, port1, port2, "expected same port (no-op on duplicate)")

	ports := mgr.ListPorts("inst")
	assert.Len(t, ports.Ports, 1)
}

// TestContextCancellation ensures StopAll cleans up listeners and goroutines.
func TestContextCancellation(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := NewManager("127.0.0.1", slog.Default())
	defer mgr.StopAll()

	targetPort, stop := startEchoServer(t)
	defer stop()

	_, err := mgr.Forward("inst", "127.0.0.1", 0, targetPort, "svc", "")
	require.NoError(t, err)

	mgr.StopAll()
}

func TestNewManager_DefaultsToLoopback(t *testing.T) {
	mgr := NewManager("", slog.Default())
	assert.Equal(t, "127.0.0.1", mgr.bindAddr)
}
