package main

import (
	"context"
	"fmt"
	"os/exec"
)

// initHandler verifies the docker CLI is on PATH (the daemon expects it
// available for plugin runtime).
func initHandler(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker CLI not found in PATH: %w", err)
	}
	return nil
}
