//go:build integration

// Package echo contains integration tests for the dockersnap echo workload
// plugin. They exercise the full plugin pipeline (validate, deploy, access,
// describe, health, teardown) end-to-end against a real dockersnap daemon.
//
// Build and deploy:
//
//	task e2e:plugins:echo
package echo

import (
	"context"
	"os"
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
	setup := integrationutil.Bootstrap("echo")
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
	_ = c.Delete(ctx, name)
}
