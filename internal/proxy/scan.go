package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/johnbuluba/dockersnap/internal/dockerd"
)

// ScanAndForward discovers published ports from a running instance's Docker daemon
// and starts forwarding them.
func (m *Manager) ScanAndForward(ctx context.Context, instanceName, socket, nsIP string) error {
	log := m.logger.With("instance", instanceName)

	nsName := dockerd.NetnsName(instanceName)

	listCmd := exec.CommandContext(ctx, "docker", "-H", "unix://"+socket, "ps", "-q")
	out, err := listCmd.Output()
	if err != nil {
		log.Debug("could not list containers for port scan", "error", err)
		return nil
	}

	containerIDs := strings.Fields(strings.TrimSpace(string(out)))
	if len(containerIDs) == 0 {
		return nil
	}

	for _, cid := range containerIDs {
		inspectCmd := exec.CommandContext(ctx, "docker", "-H", "unix://"+socket,
			"inspect", "--format", "{{json .NetworkSettings.Ports}}", cid)
		out, err := inspectCmd.Output()
		if err != nil {
			continue
		}

		var ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		}
		if err := json.Unmarshal(out, &ports); err != nil {
			continue
		}

		for containerPortProto, bindings := range ports {
			for _, b := range bindings {
				if b.HostPort == "" {
					continue
				}

				hostPort, err := strconv.Atoi(b.HostPort)
				if err != nil || hostPort == 0 {
					continue
				}

				containerPort := parseContainerPort(containerPortProto)

				cidShort := cid
				if len(cidShort) > 12 {
					cidShort = cidShort[:12]
				}
				desc := fmt.Sprintf("container:%s/%s", cidShort, containerPortProto)
				if containerPort == 6443 {
					desc = "kubernetes-api"
				}

				addNetnsDNAT(ctx, nsName, nsIP, hostPort, log)

				// dialPort = hostPort because docker-proxy inside the netns
				// listens on the docker-published port (== hostPort).
				// containerPort (parsed from "5678/tcp") is the actual
				// in-container port — surfaced as InContainerPort metadata
				// so plugin authors can match by it.
				if _, err := m.ForwardWithMetadata(instanceName, nsIP, hostPort, hostPort, containerPort, desc, nsName); err != nil {
					log.Warn("failed to forward port",
						"host_port", hostPort,
						"container_port", containerPort,
						"error", err,
					)
				}
			}
		}
	}

	return nil
}

// parseContainerPort extracts the port number from a Docker port spec like "6443/tcp".
func parseContainerPort(spec string) int {
	if i := strings.IndexByte(spec, '/'); i > 0 {
		spec = spec[:i]
	}
	port, err := strconv.Atoi(spec)
	if err != nil {
		return 0
	}
	return port
}

// addNetnsDNAT adds a PREROUTING DNAT rule inside a network namespace that redirects
// traffic arriving on the veth IP to 127.0.0.1 (where docker-proxy listens).
func addNetnsDNAT(ctx context.Context, nsName, nsIP string, port int, log *slog.Logger) {
	portStr := strconv.Itoa(port)
	target := fmt.Sprintf("127.0.0.1:%d", port)

	checkCmd := exec.CommandContext(ctx, "ip", "netns", "exec", nsName,
		"iptables", "-t", "nat", "-C", "PREROUTING",
		"-d", nsIP, "-p", "tcp", "--dport", portStr,
		"-j", "DNAT", "--to-destination", target)
	if checkCmd.Run() == nil {
		return
	}

	addCmd := exec.CommandContext(ctx, "ip", "netns", "exec", nsName,
		"iptables", "-t", "nat", "-A", "PREROUTING",
		"-d", nsIP, "-p", "tcp", "--dport", portStr,
		"-j", "DNAT", "--to-destination", target)
	if out, err := addCmd.CombinedOutput(); err != nil {
		log.Warn("failed to add DNAT rule in namespace",
			"nsName", nsName, "port", port, "error", err, "output", string(out))
	} else {
		log.Info("added DNAT rule in namespace", "nsName", nsName, "nsIP", nsIP, "port", port)
	}
}

// ScanFromHostConfig discovers published ports from container hostconfig.json files
// on disk (before dockerd starts).
func (m *Manager) ScanFromHostConfig(instanceName, dataRoot, nsIP string, logger *slog.Logger) {
	if logger == nil {
		logger = m.logger
	}
	log := logger.With("instance", instanceName)

	containersDir := dataRoot + "/containers"
	entries, err := os.ReadDir(containersDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		hcPath := containersDir + "/" + entry.Name() + "/hostconfig.json"
		data, err := os.ReadFile(hcPath)
		if err != nil {
			continue
		}

		var hc struct {
			PortBindings map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
		}
		if err := json.Unmarshal(data, &hc); err != nil {
			continue
		}

		for containerPortProto, bindings := range hc.PortBindings {
			for _, b := range bindings {
				if b.HostPort == "" {
					continue
				}

				hostPort, err := strconv.Atoi(b.HostPort)
				if err != nil || hostPort == 0 {
					continue
				}

				containerPort := parseContainerPort(containerPortProto)

				name := entry.Name()
				if len(name) > 12 {
					name = name[:12]
				}
				desc := fmt.Sprintf("container:%s/%s", name, containerPortProto)
				if containerPort == 6443 {
					desc = "kubernetes-api"
				}

				if _, err := m.ForwardWithMetadata(instanceName, nsIP, hostPort, hostPort, containerPort, desc, dockerd.NetnsName(instanceName)); err != nil {
					log.Warn("failed to forward port from hostconfig",
						"host_port", hostPort,
						"error", err,
					)
				}
			}
		}
	}
}
