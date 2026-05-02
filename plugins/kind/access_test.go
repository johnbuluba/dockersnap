package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPatchKubeconfig_ReplacesServerWithTokens(t *testing.T) {
	in := `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: BASE64DATA==
    server: https://127.0.0.1:34567
  name: kind-demo
`
	out := patchKubeconfig(in)

	assert.Contains(t, out, "server: https://${HOST}:${PORT:kubernetes-api}",
		"server URL should use template tokens")
	assert.NotContains(t, out, "https://127.0.0.1:34567")
	assert.NotContains(t, out, "certificate-authority-data:")
	assert.Contains(t, out, "insecure-skip-tls-verify: true")
}

func TestPatchKubeconfig_PreservesIndentation(t *testing.T) {
	in := "    certificate-authority-data: ABC\n"
	out := patchKubeconfig(in)
	// 4-space indent preserved on the replacement line.
	assert.True(t, strings.HasPrefix(out, "    insecure-skip-tls-verify: true"),
		"indentation must be preserved when replacing certificate-authority-data, got %q", out)
}

func TestPatchKubeconfig_NoServerLine(t *testing.T) {
	in := "no kubeconfig content here\n"
	out := patchKubeconfig(in)
	// No 127.0.0.1:port to replace — output is unchanged.
	assert.Equal(t, in, out)
}

func TestPatchKubeconfig_OnlyOneServerReplacement(t *testing.T) {
	// kind only emits one server URL, but we verify our impl does too.
	in := `server: https://127.0.0.1:1
otherline: https://127.0.0.1:2
`
	out := patchKubeconfig(in)
	// Only the first occurrence is replaced (consistent with strings.Index).
	assert.Contains(t, out, "${HOST}:${PORT:kubernetes-api}")
	assert.Contains(t, out, "https://127.0.0.1:2",
		"second occurrence is intentionally untouched — kind only emits one")
}
