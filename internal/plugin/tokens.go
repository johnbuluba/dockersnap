package plugin

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// portTokenRe matches ${PORT:<label>} where label is anything other than '}'.
var portTokenRe = regexp.MustCompile(`\$\{PORT:([^}]+)\}`)

// TokenContext is what core needs to resolve AccessResponse tokens.
type TokenContext struct {
	Host string // resolved ${HOST}
	// Ports maps label → host_port. Pulled from the proxy's current state.
	Ports map[string]int
}

// ResolveAccess substitutes ${HOST} and ${PORT:<label>} tokens in every
// File content, Env value, and synthesized Endpoint URL. ${ACCESS_DIR} is
// left alone — the CLI resolves it to its local path.
//
// Returns a new AccessResponse; the input is not mutated.
func ResolveAccess(in *pluginsdk.AccessResponse, tc TokenContext) *pluginsdk.AccessResponse {
	out := &pluginsdk.AccessResponse{
		ContractVersion: in.ContractVersion,
		Env:             make(map[string]string, len(in.Env)),
		Files:           make([]pluginsdk.File, len(in.Files)),
		Endpoints:       make([]pluginsdk.Endpoint, len(in.Endpoints)),
	}

	for k, v := range in.Env {
		out.Env[k] = substitute(v, tc)
	}
	for i, f := range in.Files {
		out.Files[i] = pluginsdk.File{
			Name:    f.Name,
			Content: substitute(f.Content, tc),
			Mode:    f.Mode,
		}
	}
	for i, ep := range in.Endpoints {
		copy := ep
		if copy.HostPortLabel != "" {
			if port, ok := tc.Ports[copy.HostPortLabel]; ok {
				scheme := copy.Scheme
				if scheme == "" {
					scheme = "https"
				}
				copy.URL = fmt.Sprintf("%s://%s:%d", scheme, tc.Host, port)
			}
		}
		// If the plugin emitted a literal URL, run substitution on it too.
		copy.URL = substitute(copy.URL, tc)
		out.Endpoints[i] = copy
	}
	return out
}

func substitute(s string, tc TokenContext) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, pluginsdk.HostToken, tc.Host)
	s = portTokenRe.ReplaceAllStringFunc(s, func(match string) string {
		// match is "${PORT:label}"
		label := match[len("${PORT:") : len(match)-1]
		if port, ok := tc.Ports[label]; ok {
			return fmt.Sprintf("%d", port)
		}
		// Unknown label — leave the token in place so the consumer can spot it.
		return match
	})
	return s
}
