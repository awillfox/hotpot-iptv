package hls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/probe"
)

func probes() []probe.Result {
	return []probe.Result{
		{ // file A: tha+eng audio, tha sub
			Audio: []probe.AudioTrack{{Index: 0, Lang: "tha"}, {Index: 1, Lang: "eng"}},
			Subs:  []probe.SubtitleTrack{{Index: 0, Lang: "tha"}},
		},
		{ // file B: eng only, two eng subs
			Audio: []probe.AudioTrack{{Index: 0, Lang: "eng"}},
			Subs:  []probe.SubtitleTrack{{Index: 0, Lang: "eng"}, {Index: 1, Lang: "eng"}},
		},
	}
}

func TestComputeRenditions(t *testing.T) {
	rends := ComputeRenditions(probes())
	keys := make([]string, 0, len(rends))
	for _, r := range rends {
		keys = append(keys, r.Key)
	}
	assert.Equal(t, []string{"v", "a_tha_0", "a_eng_0", "s_tha_0", "s_eng_0", "s_eng_1"}, keys)
	assert.Equal(t, "Thai", rends[1].Name)
	assert.Equal(t, "English 2", rends[5].Name) // second eng sub
	assert.Equal(t, "v.m3u8", rends[0].PlaylistURI())
}

func TestMapTracks(t *testing.T) {
	ps := probes()
	rends := ComputeRenditions(ps)

	mA := MapTracks(rends, ps[0])
	assert.Equal(t, 0, mA["v"])
	assert.Equal(t, 0, mA["a_tha_0"])
	assert.Equal(t, 1, mA["a_eng_0"])
	assert.Equal(t, 0, mA["s_tha_0"])
	assert.Equal(t, -1, mA["s_eng_0"]) // filler
	assert.Equal(t, -1, mA["s_eng_1"])

	mB := MapTracks(rends, ps[1])
	assert.Equal(t, -1, mB["a_tha_0"]) // filler → silence
	assert.Equal(t, 0, mB["a_eng_0"])
	assert.Equal(t, 0, mB["s_eng_0"])
	assert.Equal(t, 1, mB["s_eng_1"])
	require.Equal(t, -1, mB["s_tha_0"])
}
