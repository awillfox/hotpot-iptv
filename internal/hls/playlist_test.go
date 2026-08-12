package hls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager(window int) *Manager {
	rends := []Rendition{
		{Kind: KindVideo, Key: "v", Name: "Video"},
		{Kind: KindAudio, Key: "a_tha_0", Lang: "tha", Name: "Thai"},
		{Kind: KindAudio, Key: "a_eng_0", Lang: "eng", Name: "English"},
		{Kind: KindSubs, Key: "s_tha_0", Lang: "tha", Name: "Thai"},
	}
	return NewManager(rends, 4, window, VideoParams{Width: 1920, Height: 1080, BitrateK: 5000})
}

func TestRenderMediaWithDiscontinuity(t *testing.T) {
	m := newTestManager(10)
	m.Append("v", "000001/v_0.ts", 4.0)
	m.Append("v", "000001/v_1.ts", 3.2)
	m.MarkDiscontinuity()
	m.Append("v", "000002/v_0.ts", 4.0)

	got, ok := m.RenderMedia("v")
	require.True(t, ok)
	assert.Equal(t, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-DISCONTINUITY-SEQUENCE:0
#EXTINF:4.000,
000001/v_0.ts
#EXTINF:3.200,
000001/v_1.ts
#EXT-X-DISCONTINUITY
#EXTINF:4.000,
000002/v_0.ts
`, got)

	_, ok = m.RenderMedia("nope")
	assert.False(t, ok)
}

func TestWindowEvictionAndSequences(t *testing.T) {
	m := newTestManager(2)
	ev := m.Append("v", "000001/v_0.ts", 4)
	assert.Empty(t, ev)
	m.MarkDiscontinuity()
	m.Append("v", "000002/v_0.ts", 4)
	ev = m.Append("v", "000002/v_1.ts", 4) // evicts v_0 (no discont)
	assert.Equal(t, []string{"000001/v_0.ts"}, ev)

	got, _ := m.RenderMedia("v")
	assert.Contains(t, got, "#EXT-X-MEDIA-SEQUENCE:1")
	assert.Contains(t, got, "#EXT-X-DISCONTINUITY-SEQUENCE:0")
	assert.Contains(t, got, "#EXT-X-DISCONTINUITY\n") // 000002/v_0 still marked

	ev = m.Append("v", "000002/v_2.ts", 4) // evicts the discontinuity-marked seg
	assert.Equal(t, []string{"000002/v_0.ts"}, ev)
	got, _ = m.RenderMedia("v")
	assert.Contains(t, got, "#EXT-X-MEDIA-SEQUENCE:2")
	assert.Contains(t, got, "#EXT-X-DISCONTINUITY-SEQUENCE:1")
}

func TestRenderMaster(t *testing.T) {
	m := newTestManager(10)
	assert.Equal(t, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Thai",LANGUAGE="tha",DEFAULT=YES,AUTOSELECT=YES,URI="a_tha_0.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English",LANGUAGE="eng",DEFAULT=NO,AUTOSELECT=YES,URI="a_eng_0.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Thai",LANGUAGE="tha",DEFAULT=NO,AUTOSELECT=YES,URI="s_tha_0.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=5660000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio",SUBTITLES="subs"
v.m3u8
`, m.RenderMaster())
}
