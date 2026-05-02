package main

import (
	"context"
	"fmt"
	"time"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// validateHandler runs semantic checks the daemon's schema validation can't
// do (file existence is already checked by the daemon for TypePath; we
// add timeout-format validation here).
func validateHandler(ctx context.Context, in *pluginsdk.Context) ([]string, error) {
	var warnings []string

	if to, ok := in.Config.OptString("wait_timeout"); ok && to != "" {
		if _, err := time.ParseDuration(to); err != nil {
			return warnings, fmt.Errorf("wait_timeout %q is not a valid Go duration: %w", to, err)
		}
	}

	if v, ok := in.Config.OptString("kubernetes_version"); !ok || v == "" {
		warnings = append(warnings, "kubernetes_version not specified; will use kind's default")
	}

	if in.Config.Path("kind_config") == "" {
		warnings = append(warnings, "kind_config not specified; using kind's built-in single-node default")
	}

	return warnings, nil
}
