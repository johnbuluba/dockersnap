//go:build integration

// Package kind contains integration tests for the dockersnap kind workload
// plugin. They run on a host with dockersnap deployed and validate that a
// kind cluster can be deployed, snapshotted, reverted, and cloned via
// dockersnap's plugin pipeline.
//
// Build and deploy:
//
//	task e2e:plugins:kind
//
// (Currently the suite still shell-out to `kind create cluster` directly
// to mirror the legacy e2e tests; rewriting on top of `--workload kind-default`
// is tracked separately.)
package kind

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
	setup := integrationutil.Bootstrap("kind")
	cfg = setup.Cfg
	c = setup.Client
	prefix = setup.Prefix
	os.Exit(m.Run())
}

func instName(suffix string) string {
	return prefix + "-" + suffix
}

func cleanup(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := c.Delete(ctx, name); err != nil {
		if !strings.Contains(err.Error(), "instance not found") {
			t.Logf("cleanup(%s) failed: %v", name, err)
		}
	}
}
