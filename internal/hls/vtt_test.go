package hls

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleVTT = `WEBVTT

NOTE a comment

1
00:00:01.000 --> 00:00:03.500 align:start
Hello there

00:00:05.000 --> 00:00:09.200
Second cue
spanning two lines

00:01:02.000 --> 00:01:04.000
Late cue
`

func TestParseVTT(t *testing.T) {
	cues, err := ParseVTT(strings.NewReader(sampleVTT))
	require.NoError(t, err)
	require.Len(t, cues, 3)
	assert.Equal(t, time.Second, cues[0].Start)
	assert.Equal(t, 3500*time.Millisecond, cues[0].End)
	assert.Equal(t, "align:start", cues[0].Settings)
	assert.Equal(t, "Hello there", cues[0].Text)
	assert.Equal(t, "Second cue\nspanning two lines", cues[1].Text)
}

func TestSplitVTT(t *testing.T) {
	cues, err := ParseVTT(strings.NewReader(sampleVTT))
	require.NoError(t, err)

	segs := SplitVTT(cues, 4*time.Second, 66*time.Second)
	require.Len(t, segs, 17) // ceil(66/4)

	for _, s := range segs {
		assert.True(t, strings.HasPrefix(s,
			"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n"))
	}
	// seg 0 covers [0,4): first cue only
	assert.Contains(t, segs[0], "Hello there")
	assert.NotContains(t, segs[0], "Second cue")
	// cue 2 spans [5,9.2) → overlaps segs 1 and 2
	assert.Contains(t, segs[1], "Second cue")
	assert.Contains(t, segs[2], "Second cue")
	assert.NotContains(t, segs[3], "Second cue")
	// late cue [62,64) → seg 15
	assert.Contains(t, segs[15], "Late cue")
	// empty middle segment has only the header
	assert.Equal(t,
		"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n", segs[5])
}

func TestSplitVTTEmpty(t *testing.T) {
	segs := SplitVTT(nil, 4*time.Second, 10*time.Second)
	require.Len(t, segs, 3)
}
