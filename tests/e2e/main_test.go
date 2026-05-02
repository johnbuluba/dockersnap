//go:build integration

// Package e2e contains end-to-end tests for dockersnap's core instance
// lifecycle (ZFS, dockerd, network namespace, iptables, systemd unit).
// Plugin-specific integration tests live under tests/plugins/<name>/.
//
// These tests run ON the VM with direct system access. Build and deploy:
//
//	task e2e
package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/johnbuluba/dockersnap/internal/client"
	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/tests/integrationutil"
)

var (
	cfg    *config.Config
	c      *client.Client
	prefix string
)

func TestMain(m *testing.M) {
	setup := integrationutil.Bootstrap("e2e")
	cfg = setup.Cfg
	c = setup.Client
	prefix = setup.Prefix
	_ = setup // referenced via package-level vars for backward compat
	os.Exit(m.Run())
}

// instName returns "<prefix>-<suffix>" — the pattern used everywhere in
// this suite so the test setup tag (PID-bound) propagates to every instance.
func instName(suffix string) string {
	return prefix + "-" + suffix
}

func cleanup(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.Delete(ctx, name); err != nil {
		// "instance not found" is the normal pre-test cleanup case; anything
		// else (most commonly: dataset has dependent clones, so the source
		// can't be destroyed) means we're leaking. Surface it so the next
		// run doesn't trip over a stale instance silently.
		if !strings.Contains(err.Error(), "instance not found") {
			t.Logf("cleanup(%s) failed: %v", name, err)
		}
	}
}
