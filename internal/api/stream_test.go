package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWantsStream(t *testing.T) {
	tests := []struct {
		accept string
		want   bool
	}{
		{"application/x-ndjson", true},
		{"application/json", false},
		{"", false},
		{"text/html", false},
		// Strict equality is intentional — we don't accept comma lists today.
		{"application/x-ndjson, */*", false},
	}

	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			r := &http.Request{Header: http.Header{}}
			if tt.accept != "" {
				r.Header.Set("Accept", tt.accept)
			}
			assert.Equal(t, tt.want, wantsStream(r))
		})
	}
}
