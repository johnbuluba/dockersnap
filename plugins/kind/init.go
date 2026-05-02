package main

import (
	"context"
	"fmt"
	"os/exec"
)

// initHandler verifies the binaries we depend on exist on the daemon host.
// Called once at daemon startup.
func initHandler(ctx context.Context) error {
	for _, bin := range []string{"kind", "kubectl", "docker"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("required binary %q not found in PATH: %w", bin, err)
		}
	}
	return nil
}
