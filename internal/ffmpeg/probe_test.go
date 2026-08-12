package ffmpeg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProbeOutput(t *testing.T) {
	raw, err := os.ReadFile("testdata/probe_movie.json")
	require.NoError(t, err)

	p, err := parseProbeOutput(raw)
	require.NoError(t, err)

	assert.Equal(t, int64(5401760), p.DurationMs)
	assert.Equal(t, "hevc", p.VideoCodec)
	assert.Equal(t, 1920, p.Width)

	require.Len(t, p.Audio, 2)
	assert.Equal(t, AudioTrack{Index: 0, Lang: "tha", Codec: "eac3", Channels: 6}, p.Audio[0])
	assert.Equal(t, AudioTrack{Index: 1, Lang: "eng", Codec: "aac", Channels: 2}, p.Audio[1])

	// PGS (bitmap) is skipped; only text subs survive.
	require.Len(t, p.Subs, 1)
	assert.Equal(t, SubtitleTrack{Index: 0, Lang: "tha", Codec: "subrip"}, p.Subs[0])
}

func TestFindExternalSubs(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mkv")
	require.NoError(t, os.WriteFile(video, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "movie.eng.srt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.srt"), []byte("x"), 0o644))

	subs := findExternalSubs(video)
	require.Len(t, subs, 2)
	assert.Equal(t, "eng", subs[0].Lang) // movie.eng.srt < movie.srt in lexical order
	assert.Equal(t, "und", subs[1].Lang)
	for _, s := range subs {
		assert.True(t, s.External)
		assert.Equal(t, -1, s.Index)
		assert.Equal(t, "srt", s.Codec)
	}
}
