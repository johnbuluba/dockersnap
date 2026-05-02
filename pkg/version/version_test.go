package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersion_DefaultIsValidSemver(t *testing.T) {
	// Default value when ldflags aren't applied — must still be parsable
	// as a sane version string for tooling that introspects --version.
	assert.NotEmpty(t, Version)
	assert.True(t, len(Version) > 1 && Version[0] == 'v',
		"default Version should start with 'v', got %q", Version)
}

func TestString_ReturnsVersion(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = "v0.5.2+main.abc1234"
	assert.Equal(t, "v0.5.2+main.abc1234", String())

	Version = "v0.5.2+main.abc1234.dirty"
	assert.Equal(t, "v0.5.2+main.abc1234.dirty", String())
}
