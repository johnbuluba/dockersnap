package plugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

func TestResolveAccess_HostAndPortInFiles(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		ContractVersion: "1",
		Files: []pluginsdk.File{
			{
				Name:    "kubeconfig",
				Content: "server: https://${HOST}:${PORT:kubernetes-api}",
				Mode:    "0600",
			},
		},
	}
	tc := TokenContext{
		Host:  "10.0.0.5",
		Ports: map[string]int{"kubernetes-api": 34567},
	}
	out := ResolveAccess(in, tc)

	require.Len(t, out.Files, 1)
	assert.Equal(t, "server: https://10.0.0.5:34567", out.Files[0].Content)
	assert.Equal(t, "0600", out.Files[0].Mode, "mode should be unchanged")
}

func TestResolveAccess_LeavesAccessDirAlone(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Env: map[string]string{
			"KUBECONFIG": "${ACCESS_DIR}/kubeconfig",
		},
	}
	out := ResolveAccess(in, TokenContext{Host: "x", Ports: nil})
	assert.Equal(t, "${ACCESS_DIR}/kubeconfig", out.Env["KUBECONFIG"],
		"core must not resolve ${ACCESS_DIR} — that's the CLI's job")
}

func TestResolveAccess_EnvSubstitution(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Env: map[string]string{
			"FOO": "${HOST}:${PORT:web}",
		},
	}
	out := ResolveAccess(in, TokenContext{
		Host:  "myhost",
		Ports: map[string]int{"web": 8080},
	})
	assert.Equal(t, "myhost:8080", out.Env["FOO"])
}

func TestResolveAccess_EndpointURLSynthesis(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Endpoints: []pluginsdk.Endpoint{
			{
				Name:          "kubernetes-api",
				Scheme:        "https",
				HostPortLabel: "kubernetes-api",
				Insecure:      true,
			},
		},
	}
	tc := TokenContext{
		Host:  "10.0.0.5",
		Ports: map[string]int{"kubernetes-api": 34567},
	}
	out := ResolveAccess(in, tc)
	require.Len(t, out.Endpoints, 1)
	assert.Equal(t, "https://10.0.0.5:34567", out.Endpoints[0].URL)
	assert.Equal(t, true, out.Endpoints[0].Insecure)
}

func TestResolveAccess_EndpointDefaultsToHTTPS(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Endpoints: []pluginsdk.Endpoint{
			{Name: "x", HostPortLabel: "k", Scheme: ""},
		},
	}
	tc := TokenContext{Host: "h", Ports: map[string]int{"k": 1}}
	out := ResolveAccess(in, tc)
	assert.Equal(t, "https://h:1", out.Endpoints[0].URL)
}

func TestResolveAccess_UnknownPortLabelKeepsPlaceholder(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Files: []pluginsdk.File{
			{Content: "${PORT:not-yet-allocated}"},
		},
	}
	out := ResolveAccess(in, TokenContext{Host: "h", Ports: nil})
	assert.Equal(t, "${PORT:not-yet-allocated}", out.Files[0].Content,
		"unknown port labels should remain as tokens so callers can spot them")
}

func TestResolveAccess_DoesNotMutateInput(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Files: []pluginsdk.File{
			{Content: "${HOST}:${PORT:k}"},
		},
		Env: map[string]string{"FOO": "${HOST}"},
	}
	_ = ResolveAccess(in, TokenContext{Host: "x", Ports: map[string]int{"k": 1}})

	assert.Equal(t, "${HOST}:${PORT:k}", in.Files[0].Content, "input must not be mutated")
	assert.Equal(t, "${HOST}", in.Env["FOO"], "input must not be mutated")
}

func TestResolveAccess_HandlesMultipleHostTokens(t *testing.T) {
	in := &pluginsdk.AccessResponse{
		Files: []pluginsdk.File{
			{Content: "first: ${HOST}, second: ${HOST}"},
		},
	}
	out := ResolveAccess(in, TokenContext{Host: "h", Ports: nil})
	assert.Equal(t, "first: h, second: h", out.Files[0].Content)
}
