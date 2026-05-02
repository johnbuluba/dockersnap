package pluginsdk

import "fmt"

// Token literals plugins emit in AccessResponse.
//
// Core resolves HostToken and PortToken before responding to the API client;
// the CLI resolves AccessDirToken locally to ~/.dockersnap/<instance>/.
//
// Plugins should use these constants (or PortToken("label")) rather than
// hand-typing the strings, so renames stay safe.
const (
	HostToken      = "${HOST}"
	AccessDirToken = "${ACCESS_DIR}"
)

// PortToken returns the placeholder for a forwarded-port host port lookup.
// The label must match a Description from the host-side TCP proxy's
// PortMapping (e.g. "kubernetes-api").
//
//	server: https://${HOST}:${PORT:kubernetes-api}
func PortToken(label string) string {
	return fmt.Sprintf("${PORT:%s}", label)
}
