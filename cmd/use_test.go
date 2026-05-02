package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractHost(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"http url with port", "http://10.0.0.1:9847", "10.0.0.1"},
		{"https url with port", "https://my.host.example.com:9847", "my.host.example.com"},
		{"http url no port", "http://10.0.0.1", "10.0.0.1"},
		{"with path", "http://10.0.0.1:9847/api/v1", "10.0.0.1"},
		{"raw host:port", "10.0.0.1:9847", "10.0.0.1"},
		{"raw host", "10.0.0.1", "10.0.0.1"},
		{"localhost", "http://localhost:9847", "localhost"},
		{"ipv6 url", "http://[::1]:9847", "::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractHost(tt.in))
		})
	}
}

func TestParseTags(t *testing.T) {
	t.Run("nil for empty input", func(t *testing.T) {
		assert.Nil(t, parseTags(nil))
		assert.Nil(t, parseTags([]string{}))
	})

	t.Run("simple key=value", func(t *testing.T) {
		got := parseTags([]string{"version=2.5.0", "env=prod"})
		assert.Equal(t, "2.5.0", got["version"])
		assert.Equal(t, "prod", got["env"])
	})

	t.Run("missing equals becomes empty value", func(t *testing.T) {
		got := parseTags([]string{"flag"})
		_, ok := got["flag"]
		assert.True(t, ok)
		assert.Equal(t, "", got["flag"])
	})

	t.Run("equals in value preserved", func(t *testing.T) {
		got := parseTags([]string{"checksum=sha256=abc123"})
		assert.Equal(t, "sha256=abc123", got["checksum"])
	})
}
