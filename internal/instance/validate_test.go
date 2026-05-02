package instance

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", true},
		{"valid simple", "env1", false},
		{"valid with hyphen", "test-env-1", false},
		{"valid all letters", "abc", false},
		{"max length 32", "a23456789012345678901234567890ab", false},
		{"too long", "a234567890123456789012345678901234", true},
		{"leading digit", "1env", true},
		{"leading hyphen", "-env", true},
		{"uppercase", "Env1", true},
		{"slash", "env/1", true},
		{"dot dot", "..", true},
		{"semicolon", "env;rm", true},
		{"shell substitution", "$(rm)", true},
		{"underscore", "env_1", true},
		{"space", "env 1", true},
		{"at sign", "env@snap", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateName(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
