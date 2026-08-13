package epg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sched(startedAgo time.Duration, now time.Time) ChannelSchedule {
	return ChannelSchedule{
		Slug: "movies", Name: "Movies", Number: 1,
		Items: []Item{
			{Title: "First", DurationMs: 3600_000},
			{Title: "Second", DurationMs: 1800_000},
		},
		CurrentPos:    0,
		ItemStartedAt: now.Add(-startedAgo),
	}
}

func TestForward(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	entries := Forward(sched(30*time.Minute, now), now, 3*time.Hour)

	require.NotEmpty(t, entries)
	// The in-progress item is included from its real start, not from now —
	// a player needs to know how much of it is left.
	assert.Equal(t, "First", entries[0].Title)
	assert.Equal(t, now.Add(-30*time.Minute), entries[0].Start)
	assert.Equal(t, now.Add(30*time.Minute), entries[0].Stop)
	assert.Equal(t, "Second", entries[1].Title)

	// The playlist loops, so the guide keeps filling until the horizon.
	last := entries[len(entries)-1]
	assert.True(t, last.Start.Before(now.Add(3*time.Hour)))
	assert.True(t, last.Stop.After(now.Add(3*time.Hour)) || last.Stop.Equal(now.Add(3*time.Hour)))
}

func TestForwardStoppedChannelStartsNow(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	cs := sched(0, now)
	cs.ItemStartedAt = time.Time{} // never run
	entries := Forward(cs, now, time.Hour)
	require.NotEmpty(t, entries)
	assert.Equal(t, now, entries[0].Start)
}

func TestForwardZeroDuration(t *testing.T) {
	now := time.Now()
	cs := ChannelSchedule{Slug: "x", Items: []Item{{Title: "a", DurationMs: 0}}}
	// Without this guard the loop below would never advance and would hang.
	assert.Nil(t, Forward(cs, now, time.Hour))
}

func TestRenderXMLTV(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	out := RenderXMLTV([]ChannelSchedule{sched(0, now)}, now, time.Hour)
	assert.Contains(t, out, `<?xml version="1.0" encoding="UTF-8"?>`)
	assert.Contains(t, out, `<channel id="movies">`)
	assert.Contains(t, out, `<display-name>Movies</display-name>`)
	assert.Contains(t, out, `start="20260812200000 +0000"`)
	assert.Contains(t, out, `<title>First</title>`)
}

func TestRenderM3U(t *testing.T) {
	out := RenderM3U("http://box:8080", []ChannelSchedule{sched(0, time.Now())})
	assert.Equal(t, `#EXTM3U
#EXTINF:-1 tvg-id="movies" tvg-name="Movies" tvg-chno="1",Movies
http://box:8080/streams/movies/master.m3u8
`, out)
}

func TestXMLEscaping(t *testing.T) {
	now := time.Now().UTC()
	cs := ChannelSchedule{Slug: "x", Name: "A&B", Items: []Item{{Title: "<fun>", DurationMs: 60000}}}
	out := RenderXMLTV([]ChannelSchedule{cs}, now, time.Minute)
	assert.Contains(t, out, "A&amp;B")
	assert.Contains(t, out, "&lt;fun&gt;")
	assert.NotContains(t, out, "<fun>", "an unescaped title would corrupt the guide")
}
