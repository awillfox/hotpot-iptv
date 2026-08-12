package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "/media", cfg.MediaPath)
	assert.Equal(t, 4, cfg.SegmentSeconds)
	assert.Equal(t, 30, cfg.WindowSegments)
	assert.Equal(t, "nvenc", cfg.Encoder)
	assert.Equal(t, 5000, cfg.VideoBitrateK)
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("PORT", "9099")
	t.Setenv("ENCODER", "software")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9099, cfg.Port)
	assert.Equal(t, "software", cfg.Encoder)
}
