// Package version exposes the build-time version string for a dockersnap
// binary (the daemon, CLI, or any plugin built from this repo).
//
// Version is a valid SemVer 2.0 string with build metadata:
//
//	v0.5.2+main.abc1234
//	v0.5.2+main.abc1234.dirty
//	v0.0.0+feature-foo.abc1234.dirty   (no tags reachable from HEAD)
//
// The Taskfile composes it from `git describe --tags --abbrev=0`, the
// branch name, the short commit hash, and a "dirty" marker when the
// working tree has uncommitted changes; it's stamped into the binary at
// link time via:
//
//	-ldflags "-X 'github.com/johnbuluba/dockersnap/pkg/version.Version=...'"
//
// Plain `go build` outside Task leaves the package default in place.
package version

// Version is overridden at link time. The default reflects a build
// without ldflags (e.g. someone running `go build` directly).
var Version = "v0.0.0+dev"

// String returns the version string. Kept as a function so callers don't
// have to know whether Version is a var, const, or struct field.
func String() string { return Version }
