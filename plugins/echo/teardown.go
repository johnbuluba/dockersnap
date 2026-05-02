package main

import (
	"context"
	"fmt"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// teardownHandler removes every container labeled for this instance,
// running or stopped. Idempotent: succeeds when nothing is found.
func teardownHandler(ctx context.Context, in *pluginsdk.Context, p *pluginsdk.Progress) error {
	p.Step("finding_containers", "Searching for echo containers to remove")
	ids, err := findAllContainerIDs(ctx, in)
	if err != nil {
		return p.Fail("finding_containers", err)
	}
	if len(ids) == 0 {
		p.Done("finding_containers")
		p.Complete("no echo containers to remove")
		return nil
	}
	p.Done("finding_containers")

	p.Step("removing_containers", fmt.Sprintf("Removing %d container(s)", len(ids)))
	args := append([]string{"rm", "--force"}, ids...)
	cmd := dockerCmd(ctx, in, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return p.Fail("removing_containers", errCombined(err, out))
	}
	p.Done("removing_containers")

	p.Complete("removed %d echo container(s)", len(ids))
	return nil
}
