package instance

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrNotFound is returned by Manager methods when the named instance
// doesn't exist in the state file. Callers (typically the API server)
// should map this to HTTP 404. Use errors.Is to check.
var ErrNotFound = errors.New("instance not found")

// nameRe restricts instance names to characters safe for use in:
//   - ZFS dataset paths (no '/', '@')
//   - Filesystem paths (no '/', '..')
//   - systemd unit names (alphanumerics + '-')
//   - Linux network namespace names (limited to ~63 chars; we cap at 32)
//   - Linux interface names (IFNAMSIZ=15; veth name uses an 8-char hash for long names)
//   - iptables comments (no shell metas)
//
// Names must start with a letter and contain only lowercase letters, digits, and hyphens.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidateName returns an error if name is not a safe instance name.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("instance name is required")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid instance name %q: must match %s", name, nameRe.String())
	}
	return nil
}
