// Package epg computes a forward-looking program guide from each channel's
// looped playlist and renders XMLTV + M3U outputs.
package epg

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

type Item struct {
	Title      string
	DurationMs int64
}

type ChannelSchedule struct {
	Slug          string
	Name          string
	Number        int32
	Items         []Item
	CurrentPos    int
	ItemStartedAt time.Time
}

type Entry struct {
	Slug  string
	Start time.Time
	Stop  time.Time
	Title string
}

// Forward walks the looped playlist from CurrentPos until the horizon. It
// starts from the current item's real start time — which is usually in the
// past — so a player can tell how much of the in-progress programme is left.
func Forward(cs ChannelSchedule, now time.Time, horizon time.Duration) []Entry {
	if len(cs.Items) == 0 {
		return nil
	}
	// A playlist of zero-length items would never advance `start`, so the loop
	// below would spin forever. Refuse it up front.
	var total int64
	for _, it := range cs.Items {
		total += it.DurationMs
	}
	if total <= 0 {
		return nil
	}
	pos := cs.CurrentPos
	if pos < 0 || pos >= len(cs.Items) {
		pos = 0
	}
	start := cs.ItemStartedAt
	if start.IsZero() {
		start = now // never run: the guide is a projection from now
	}
	end := now.Add(horizon)
	var entries []Entry
	for start.Before(end) {
		it := cs.Items[pos]
		stop := start.Add(time.Duration(it.DurationMs) * time.Millisecond)
		entries = append(entries, Entry{Slug: cs.Slug, Start: start, Stop: stop, Title: it.Title})
		start = stop
		pos = (pos + 1) % len(cs.Items)
	}
	return entries
}

func xmltvTime(t time.Time) string {
	return t.Format("20060102150405 -0700")
}

// esc keeps filenames with & or <> from corrupting the guide — media titles
// are derived from user filenames, so they are not trusted markup.
func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func RenderXMLTV(schedules []ChannelSchedule, now time.Time, horizon time.Duration) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<tv generator-info-name="hotpot-iptv">` + "\n")
	// XMLTV wants every <channel> declared before any <programme>.
	for _, cs := range schedules {
		fmt.Fprintf(&b, "  <channel id=%q>\n    <display-name>%s</display-name>\n  </channel>\n",
			cs.Slug, esc(cs.Name))
	}
	for _, cs := range schedules {
		for _, e := range Forward(cs, now, horizon) {
			fmt.Fprintf(&b, "  <programme start=%q stop=%q channel=%q>\n    <title>%s</title>\n  </programme>\n",
				xmltvTime(e.Start), xmltvTime(e.Stop), e.Slug, esc(e.Title))
		}
	}
	b.WriteString("</tv>\n")
	return b.String()
}

func RenderM3U(baseURL string, schedules []ChannelSchedule) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, cs := range schedules {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=%q tvg-name=%q tvg-chno=\"%d\",%s\n%s/streams/%s/master.m3u8\n",
			cs.Slug, cs.Name, cs.Number, cs.Name, baseURL, cs.Slug)
	}
	return b.String()
}
