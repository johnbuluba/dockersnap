package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseContainerPort(t *testing.T) {
	tests := []struct {
		spec string
		want int
	}{
		{"6443/tcp", 6443},
		{"80/tcp", 80},
		{"5432", 5432},
		{"6443/udp", 6443},
		{"garbage", 0},
		{"", 0},
		{"-1/tcp", -1},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			assert.Equal(t, tt.want, parseContainerPort(tt.spec))
		})
	}
}
