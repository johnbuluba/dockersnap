package dockerd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

// NetnsName returns the network namespace name for an instance.
func NetnsName(instanceName string) string {
	return "ds-" + instanceName
}

// VethHostName returns the host-side veth interface name for an instance.
// Limited to 15 chars (Linux IFNAMSIZ). Uses a hash suffix for uniqueness
// when the name would be truncated.
func VethHostName(instanceName string) string {
	name := "ve-" + instanceName
	if len(name) <= 15 {
		return name
	}
	// Use hash to ensure uniqueness even with long names
	h := sha256.Sum256([]byte(instanceName))
	suffix := hex.EncodeToString(h[:])[:8]
	return "ve-" + suffix
}

// NetnsConfig holds the parameters for setting up a network namespace.
type NetnsConfig struct {
	InstanceName  string // Instance name (used to derive netns, veth names)
	Subnet        string // Instance's allocated subnet (e.g., "10.28.0.0/16")
	HostInterface string // Physical interface for MASQUERADE (auto-detected if empty)
	Logger        *slog.Logger
}

// SetupNetns creates an isolated network namespace for a dockersnap instance.
//
// Architecture:
//   - Creates netns "ds-<name>"
//   - Creates veth pair: veth-<name> (host) ↔ veth0 (namespace)
//   - Assigns point-to-point /30 IPs from the bottom of the instance's /16 range:
//     Host: 10.X.0.1/30, Namespace: 10.X.0.2/30
//   - Adds default route in namespace → host veth IP
//   - Adds MASQUERADE rule on host for namespace traffic
//   - Enables IP forwarding for the veth
//
// This gives each dockerd fully isolated iptables, bridges, and routing.
func SetupNetns(ctx context.Context, cfg NetnsConfig) error {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	log = log.With("instance", cfg.InstanceName)

	nsName := NetnsName(cfg.InstanceName)
	vethHost := VethHostName(cfg.InstanceName)
	// veth0 is the final name inside the namespace, but two concurrent
	// SetupNetns calls would race on `ip link add ... peer name veth0`
	// because the peer briefly exists in the host namespace before we
	// move it. Create with a unique temp name (deterministic per instance,
	// derived from the host-side veth name) and rename to veth0 once it's
	// inside the netns, where the name is unique by definition.
	vethNSTemp := "vt-" + strings.TrimPrefix(vethHost, "ve-")
	if len(vethNSTemp) > 15 {
		vethNSTemp = vethNSTemp[:15]
	}
	vethNS := "veth0"

	// Parse the second octet from subnet to derive IPs.
	// Subnet format: "10.X.0.0/16"
	hostIP, nsIP, network, err := DeriveNetnsIPs(cfg.Subnet)
	if err != nil {
		return fmt.Errorf("deriving netns IPs from subnet %q: %w", cfg.Subnet, err)
	}

	// Detect host interface if not specified
	hostIface := cfg.HostInterface
	if hostIface == "" {
		hostIface, err = detectDefaultInterface(ctx)
		if err != nil {
			return fmt.Errorf("detecting default interface: %w", err)
		}
	}

	log.Info("setting up network namespace",
		"netns", nsName,
		"veth_host", vethHost,
		"host_ip", hostIP,
		"ns_ip", nsIP,
		"host_iface", hostIface,
	)

	// Step 1: Create network namespace
	if err := run(ctx, "ip", "netns", "add", nsName); err != nil {
		return fmt.Errorf("creating netns %s: %w", nsName, err)
	}

	// Step 2: Create veth pair with a unique temp peer name (race-safe
	// against concurrent SetupNetns calls).
	if err := run(ctx, "ip", "link", "add", vethHost, "type", "veth", "peer", "name", vethNSTemp); err != nil {
		// Cleanup netns on failure
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("creating veth pair: %w", err)
	}

	// Note: the systemd-networkd "leave ve-* alone" config is written once on
	// daemon startup (cmd/serve.go calls EnsureNetworkdIgnoresVeths). We don't
	// repeat it here to avoid stat'ing /etc on every instance start.

	// Step 3: Move namespace-side veth into the netns, then rename to veth0.
	// Inside the netns the name only has to be unique per-namespace, so
	// every instance ends up with a "veth0" without colliding.
	if err := run(ctx, "ip", "link", "set", vethNSTemp, "netns", nsName); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("moving veth to netns: %w", err)
	}
	if err := run(ctx, "ip", "netns", "exec", nsName, "ip", "link", "set", vethNSTemp, "name", vethNS); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("renaming veth in netns: %w", err)
	}

	// Step 4: Configure host-side veth
	// Flush any addresses networkd may have assigned (link-local, DHCP)
	run(ctx, "ip", "addr", "flush", "dev", vethHost)
	if err := run(ctx, "ip", "addr", "add", hostIP+"/30", "dev", vethHost); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("adding IP to host veth: %w", err)
	}
	if err := run(ctx, "ip", "link", "set", vethHost, "up"); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("bringing up host veth: %w", err)
	}

	// Step 5: Configure namespace-side veth
	if err := run(ctx, "ip", "netns", "exec", nsName, "ip", "addr", "add", nsIP+"/30", "dev", vethNS); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("adding IP to ns veth: %w", err)
	}
	if err := run(ctx, "ip", "netns", "exec", nsName, "ip", "link", "set", vethNS, "up"); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("bringing up ns veth: %w", err)
	}
	if err := run(ctx, "ip", "netns", "exec", nsName, "ip", "link", "set", "lo", "up"); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("bringing up lo in netns: %w", err)
	}

	// Step 6: Default route in namespace → host veth IP
	if err := run(ctx, "ip", "netns", "exec", nsName, "ip", "route", "add", "default", "via", hostIP); err != nil {
		run(ctx, "ip", "link", "del", vethHost)
		run(ctx, "ip", "netns", "del", nsName)
		return fmt.Errorf("adding default route in netns: %w", err)
	}

	// Step 6b: Enable route_localnet on veth0 inside the namespace.
	// This allows DNAT to 127.0.0.1 for port forwarding from host to namespace loopback.
	run(ctx, "ip", "netns", "exec", nsName, "sysctl", "-w", "net.ipv4.conf.veth0.route_localnet=1")
	run(ctx, "ip", "netns", "exec", nsName, "sysctl", "-w", "net.ipv4.conf.all.route_localnet=1")

	// Step 7: Enable IP forwarding for the veth on the host
	if err := run(ctx, "sysctl", "-w", fmt.Sprintf("net.ipv4.conf.%s.forwarding=1", vethHost)); err != nil {
		log.Warn("failed to enable forwarding on veth (may already be enabled globally)", "error", err)
	}
	// Also ensure global forwarding is on
	run(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1")

	// Step 8: MASQUERADE rule for traffic from the namespace going out
	if err := run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING",
		"-s", network,
		"-o", hostIface,
		"-j", "MASQUERADE",
		"-m", "comment", "--comment", "dockersnap-"+cfg.InstanceName); err != nil {
		log.Warn("failed to add MASQUERADE rule", "error", err)
	}

	// Step 9: ACCEPT forwarding rules for the veth
	run(ctx, "iptables", "-A", "FORWARD",
		"-i", vethHost, "-o", hostIface,
		"-j", "ACCEPT",
		"-m", "comment", "--comment", "dockersnap-"+cfg.InstanceName)
	run(ctx, "iptables", "-A", "FORWARD",
		"-i", hostIface, "-o", vethHost,
		"-m", "state", "--state", "RELATED,ESTABLISHED",
		"-j", "ACCEPT",
		"-m", "comment", "--comment", "dockersnap-"+cfg.InstanceName)

	// Step 10: DNS resolution inside the namespace.
	// Set up the namespace's resolv.conf to point to the host veth IP.
	// The host (or a local dnsmasq) resolves DNS for all namespace traffic.
	// This ensures kind pods can resolve external names through the proxy.
	nsResolv := fmt.Sprintf("/etc/netns/%s", nsName)
	if err := os.MkdirAll(nsResolv, 0755); err != nil {
		log.Warn("failed to create netns resolv dir", "error", err)
	} else {
		resolvContent := fmt.Sprintf("nameserver %s\n", hostIP)
		if err := os.WriteFile(nsResolv+"/resolv.conf", []byte(resolvContent), 0644); err != nil {
			log.Warn("failed to write netns resolv.conf", "error", err)
		}
	}

	log.Info("network namespace setup complete", "netns", nsName)
	return nil
}

// TeardownNetns removes the network namespace and associated host resources.
// The veth pair is automatically deleted when the namespace is removed.
func TeardownNetns(ctx context.Context, instanceName string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	log := logger.With("instance", instanceName)

	nsName := NetnsName(instanceName)
	vethHost := VethHostName(instanceName)

	log.Info("tearing down network namespace", "netns", nsName)

	// Remove iptables rules (by comment match)
	comment := "dockersnap-" + instanceName
	removeIptablesRulesByComment(ctx, "nat", "POSTROUTING", comment)
	removeIptablesRulesByComment(ctx, "filter", "FORWARD", comment)

	// Delete the host-side veth (also removes the ns-side peer)
	run(ctx, "ip", "link", "del", vethHost)

	// Delete the network namespace
	if err := run(ctx, "ip", "netns", "del", nsName); err != nil {
		// Namespace might already be gone
		log.Warn("could not delete netns (may already be gone)", "error", err)
	}

	// Clean up per-namespace resolv.conf
	nsResolvDir := fmt.Sprintf("/etc/netns/%s", nsName)
	os.RemoveAll(nsResolvDir)

	log.Info("network namespace teardown complete", "netns", nsName)
	return nil
}

// DeriveNetnsIPs derives the host and namespace IPs from an instance subnet.
// Input: "10.X.0.0/16" → host: "10.X.0.1", ns: "10.X.0.2", network: "10.X.0.0/30"
func DeriveNetnsIPs(subnet string) (hostIP, nsIP, network string, err error) {
	var a, b int
	_, err = fmt.Sscanf(subnet, "%d.%d.", &a, &b)
	if err != nil {
		return "", "", "", fmt.Errorf("parsing subnet %q: %w", subnet, err)
	}
	hostIP = fmt.Sprintf("%d.%d.0.1", a, b)
	nsIP = fmt.Sprintf("%d.%d.0.2", a, b)
	network = fmt.Sprintf("%d.%d.0.0/30", a, b)
	return hostIP, nsIP, network, nil
}

// detectDefaultInterface finds the host's default route interface.
func detectDefaultInterface(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "ip", "route", "show", "default")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("running ip route show default: %w", err)
	}
	// Output like: "default via 10.227.216.1 dev ens192 proto static"
	fields := strings.Fields(strings.TrimSpace(string(out)))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("could not parse default interface from: %s", string(out))
}

// removeIptablesRulesByComment removes all iptables rules matching a comment.
func removeIptablesRulesByComment(ctx context.Context, table, chain, comment string) {
	// List rules with line numbers, find matching ones, delete in reverse order
	cmd := exec.CommandContext(ctx, "iptables", "-t", table, "-L", chain, "--line-numbers", "-n")
	out, err := cmd.Output()
	if err != nil {
		return
	}

	// Parse line numbers of rules with our comment
	lines := strings.Split(string(out), "\n")
	var ruleNums []string
	for _, line := range lines {
		if strings.Contains(line, comment) {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				ruleNums = append(ruleNums, fields[0])
			}
		}
	}

	// Delete in reverse order to maintain line number validity
	for i := len(ruleNums) - 1; i >= 0; i-- {
		run(ctx, "iptables", "-t", table, "-D", chain, ruleNums[i])
	}
}

// run executes a command and returns an error if it fails.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w (output: %s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// EnsureNetworkdIgnoresVeths writes a systemd-networkd config that prevents it
// from managing ve-* interfaces. Without this, networkd overrides our manually
// assigned IPs with DHCP/link-local addresses, breaking netns connectivity.
// This is idempotent — only writes if the file doesn't exist or differs.
func EnsureNetworkdIgnoresVeths(log *slog.Logger) {
	const path = "/etc/systemd/network/10-dockersnap-veth.network"
	const content = `[Match]
Name=ve-*

[Link]
Unmanaged=yes
ActivationPolicy=manual

[Network]
DHCP=no
LinkLocalAddressing=no
LLDP=no
EmitLLDP=no
IPv6AcceptRA=no
IPv6SendRA=no
`
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == content {
		return // already correct
	}

	if err := os.MkdirAll("/etc/systemd/network", 0755); err != nil {
		log.Warn("could not create /etc/systemd/network", "error", err)
		return
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		log.Warn("could not write networkd config to ignore veths", "error", err)
		return
	}
	log.Info("wrote systemd-networkd config to ignore ve-* interfaces", "path", path)

	// Remove old file if it exists (from previous version with wrong priority/format)
	os.Remove("/etc/systemd/network/90-dockersnap-veth.network")

	// Restart networkd so it fully re-evaluates (reload alone isn't sufficient)
	exec.Command("systemctl", "restart", "systemd-networkd").Run()
}
