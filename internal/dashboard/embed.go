// Package dashboard embeds the built Preact dashboard bundle so the daemon
// can serve it under /ui/* without a separate static-file deploy.
//
// The bundle is produced by `task ui:build` (which invokes `npm run build`
// inside dashboard/) and copied here by `task ui:embed` before `task build`
// links the daemon binary. The dist/ directory is gitignored — it's a
// build artifact.
package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var bundle embed.FS

// FS returns the rooted sub-filesystem (without the dist/ prefix) so the
// caller can serve /index.html and /assets/… directly. The bool reports
// whether a real bundle is present — false if `task ui:embed` hasn't been
// run, in which case the daemon should serve a stub explaining how to
// produce one rather than a 404.
func FS() (fs.FS, bool) {
	sub, err := fs.Sub(bundle, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
