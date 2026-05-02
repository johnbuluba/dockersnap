// Package integrationutil holds helpers shared across the dockersnap
// integration test suites under tests/.
//
// These tests run on the VM (or any host with a working dockersnap daemon)
// and exercise the full stack: ZFS, dockerd, network namespaces, the API,
// and — when applicable — workload plugins.
package integrationutil

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/internal/config"
)

// Defaults match the production daemon layout.
const (
	DefaultConfigPath = "/etc/dockersnap/config.yaml"
	DefaultAPIAddr    = "http://127.0.0.1:9847"
)

// Setup is the bundle returned by Bootstrap — everything an integration
// test suite needs to talk to a running daemon.
type Setup struct {
	Cfg    *config.Config
	Client *client.Client
	// Prefix is namespacing for test instances: "<suite>-<pid>".
	// Use Prefix + a per-test suffix to avoid collisions with parallel runs.
	Prefix string
}

// Bootstrap loads config, builds a client, verifies the daemon is reachable,
// and cleans up any stale instances from prior test runs that crashed.
//
// Suite is a short name like "e2e", "echo", "kind" — used as the instance
// name prefix so concurrent suites don't step on each other.
//
// Call from TestMain. On any failure the process exits with status 1.
func Bootstrap(suite string) *Setup {
	cfg, err := config.Load(DefaultConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: cannot load config from %s: %v\n", DefaultConfigPath, err)
		os.Exit(1)
	}

	c := client.New(DefaultAPIAddr, cfg.API.Token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.List(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: API not reachable at %s: %v\n", DefaultAPIAddr, err)
		os.Exit(1)
	}

	prefix := fmt.Sprintf("%s-%d", suite, os.Getpid())

	if err := cleanupStale(c, suite); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: stale-instance cleanup failed: %v\n", err)
	}

	return &Setup{Cfg: cfg, Client: c, Prefix: prefix}
}

// cleanupStale removes any instances whose names start with the suite prefix
// (e.g. "echo-" or "kind-"). Catches leftovers from prior test runs that
// crashed before t.Cleanup ran.
//
// Order matters: ZFS clones depend on the source's snapshot, so a source
// can't be destroyed while its clone exists. We sort clones first
// (anything with clone_of set) then plain instances, so a clone +
// source pair from a crashed run gets cleaned up correctly. List() has
// no defined order otherwise.
func cleanupStale(c *client.Client, suitePrefix string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	instances, err := c.List(ctx)
	if err != nil {
		return err
	}

	// Stable partition: clones first, sources second.
	var clones, sources []string
	for _, inst := range instances {
		if !strings.HasPrefix(inst.Name, suitePrefix+"-") {
			continue
		}
		if inst.CloneOf != "" {
			clones = append(clones, inst.Name)
		} else {
			sources = append(sources, inst.Name)
		}
	}

	for _, name := range append(clones, sources...) {
		fmt.Fprintf(os.Stderr, "  cleaning up stale instance: %s\n", name)
		if err := c.Delete(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "  WARNING: failed to delete %s: %v\n", name, err)
		}
	}
	return nil
}

// InstName returns "<prefix>-<suffix>" suitable for a per-test instance name.
func (s *Setup) InstName(suffix string) string {
	return fmt.Sprintf("%s-%s", s.Prefix, suffix)
}

// Cleanup deletes the named instance, ignoring not-found errors. Designed
// for t.Cleanup or defer.
func (s *Setup) Cleanup(t testing.TB, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = s.Client.Delete(ctx, name)
}
