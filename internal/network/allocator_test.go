package network

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocator_SubnetForIndex(t *testing.T) {
	tests := []struct {
		name       string
		baseOffset string
		subnetSize int
		index      int
		want       string
	}{
		{"first subnet", "10.10.0.0", 16, 0, "10.10.0.0/16"},
		{"second subnet", "10.10.0.0", 16, 1, "10.11.0.0/16"},
		{"tenth subnet", "10.10.0.0", 16, 10, "10.20.0.0/16"},
		{"overflow second octet", "10.250.0.0", 16, 10, "11.4.0.0/16"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alloc, err := NewAllocator(tt.baseOffset, tt.subnetSize)
			require.NoError(t, err)

			got := alloc.SubnetForIndex(tt.index)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMetalLBIPForSubnet(t *testing.T) {
	tests := []struct {
		name       string
		subnet     string
		hostOffset string
		want       string
		wantErr    bool
	}{
		{"primary cluster", "10.10.0.0/16", "10.10", "10.10.10.10", false},
		{"second cluster", "10.11.0.0/16", "10.10", "10.11.10.10", false},
		{"invalid subnet", "not-a-subnet", "10.10", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MetalLBIPForSubnet(tt.subnet, tt.hostOffset)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewAllocator_InvalidInput(t *testing.T) {
	_, err := NewAllocator("not-an-ip", 16)
	require.Error(t, err)
}

func TestSubnetForIndexChecked_OverflowDetected(t *testing.T) {
	// Base 250.250.0.0/16: indices that push us past 255.255 should error.
	alloc, err := NewAllocator("250.250.0.0", 16)
	require.NoError(t, err)

	// index 5 → 250 + 5 = 255 → byte 255, no carry. Valid.
	got, err := alloc.SubnetForIndexChecked(5)
	require.NoError(t, err)
	assert.Equal(t, "250.255.0.0/16", got)

	// index 6 → 250 + 6 = 256 → carry 1 into 250 → 251. Valid.
	got, err = alloc.SubnetForIndexChecked(6)
	require.NoError(t, err)
	assert.Equal(t, "251.0.0.0/16", got)

	// Push past 255.x.0.0 — should error.
	_, err = alloc.SubnetForIndexChecked(2000)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overflow")
}

func TestSubnetForIndexChecked_NegativeIndex(t *testing.T) {
	alloc, _ := NewAllocator("10.10.0.0", 16)
	_, err := alloc.SubnetForIndexChecked(-1)
	require.Error(t, err)
}
