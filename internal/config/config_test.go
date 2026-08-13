package config

import (
	"os"
	"path/filepath"
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

func TestLoadReadsDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("PORT=7777\nENCODER=software\nMEDIA_PATH=/srv/media\n"), 0o600))
	t.Chdir(dir)

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 7777, cfg.Port)
	assert.Equal(t, "software", cfg.Encoder)
	assert.Equal(t, "/srv/media", cfg.MediaPath)
}

func TestRealEnvBeatsDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("PORT=7777\n"), 0o600))
	t.Chdir(dir)
	t.Setenv("PORT", "8123")

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8123, cfg.Port, "a real env var must win over .env")
}

func TestLoadWithoutDotEnvFileIsNotAnError(t *testing.T) {
	t.Chdir(t.TempDir()) // no .env here

	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port, "defaults still apply when .env is absent")
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("PORT", "9099")
	t.Setenv("ENCODER", "software")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9099, cfg.Port)
	assert.Equal(t, "software", cfg.Encoder)
}
