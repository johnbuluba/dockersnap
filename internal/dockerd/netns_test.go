package dockerd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetnsName(t *testing.T) {
	assert.Equal(t, "ds-env1", NetnsName("env1"))
	assert.Equal(t, "ds-some-long-name", NetnsName("some-long-name"))
}

func TestVethHostName_ShortName(t *testing.T) {
	// 12 chars total ("ve-" + 9) — fits within IFNAMSIZ=15.
	assert.Equal(t, "ve-env1", VethHostName("env1"))
}

func TestVethHostName_BoundaryName(t *testing.T) {
	// Exactly 15 chars: "ve-" + 12 chars.
	assert.Equal(t, "ve-abcdefghijkl", VethHostName("abcdefghijkl"))
}

func TestVethHostName_LongNameUsesHash(t *testing.T) {
	// 13+ char names get hashed.
	got := VethHostName("a-very-long-instance-name")
	assert.True(t, len(got) <= 15, "veth name must fit IFNAMSIZ=15, got %s (%d chars)", got, len(got))
	assert.Equal(t, "ve-", got[:3])
}

func TestVethHostName_DifferentNamesProduceDifferentHashes(t *testing.T) {
	a := VethHostName("a-very-long-instance-name-aaa")
	b := VethHostName("a-very-long-instance-name-bbb")
	assert.NotEqual(t, a, b, "different long names must produce distinct veth names")
}

func TestDeriveNetnsIPs(t *testing.T) {
	tests := []struct {
		subnet    string
		wantHost  string
		wantNs    string
		wantNetwk string
		wantErr   bool
	}{
		{subnet: "10.10.0.0/16", wantHost: "10.10.0.1", wantNs: "10.10.0.2", wantNetwk: "10.10.0.0/30"},
		{subnet: "10.50.0.0/16", wantHost: "10.50.0.1", wantNs: "10.50.0.2", wantNetwk: "10.50.0.0/30"},
		{subnet: "172.20.0.0/16", wantHost: "172.20.0.1", wantNs: "172.20.0.2", wantNetwk: "172.20.0.0/30"},
		{subnet: "garbage", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.subnet, func(t *testing.T) {
			host, ns, netwk, err := DeriveNetnsIPs(tt.subnet)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantNs, ns)
			assert.Equal(t, tt.wantNetwk, netwk)
		})
	}
}
