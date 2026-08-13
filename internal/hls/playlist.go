package hls

import (
	"fmt"
	"strings"
	"sync"
)

type VideoParams struct {
	Width    int
	Height   int
	BitrateK int
}

type segment struct {
	uri     string
	dur     float64
	discont bool
}

type mediaPlaylist struct {
	seq     int64
	discSeq int64
	segs    []segment
	pending bool // next append gets a discontinuity marker
}

type Manager struct {
	mu        sync.Mutex
	rends     []Rendition
	targetDur int
	window    int
	video     VideoParams
	media     map[string]*mediaPlaylist
}

func NewManager(rends []Rendition, targetDurSec, window int, video VideoParams) *Manager {
	m := &Manager{
		rends: rends, targetDur: targetDurSec, window: window, video: video,
		media: make(map[string]*mediaPlaylist, len(rends)),
	}
	for _, r := range rends {
		m.media[r.Key] = &mediaPlaylist{}
	}
	return m
}

func (m *Manager) Renditions() []Rendition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Rendition, len(m.rends))
	copy(out, m.rends)
	return out
}

func (m *Manager) MarkDiscontinuity() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pl := range m.media {
		if len(pl.segs) > 0 { // very first segment of a stream needs no marker
			pl.pending = true
		}
	}
}

func (m *Manager) Append(key, uri string, dur float64) (evicted []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pl, ok := m.media[key]
	if !ok {
		return nil
	}
	pl.segs = append(pl.segs, segment{uri: uri, dur: dur, discont: pl.pending})
	pl.pending = false
	for len(pl.segs) > m.window {
		old := pl.segs[0]
		pl.segs = pl.segs[1:]
		pl.seq++
		if old.discont {
			pl.discSeq++
		}
		evicted = append(evicted, old.uri)
	}
	return evicted
}

// LiveURIs returns every segment URI still inside the live window, across all
// renditions. Callers use it to decide which on-disk files are still reachable.
func (m *Manager) LiveURIs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, pl := range m.media {
		for _, s := range pl.segs {
			out = append(out, s.uri)
		}
	}
	return out
}

func (m *Manager) RenderMedia(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pl, ok := m.media[key]
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", m.targetDur)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", pl.seq)
	fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", pl.discSeq)
	for _, s := range pl.segs {
		if s.discont {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", s.dur, s.uri)
	}
	return b.String(), true
}

func (m *Manager) RenderMaster() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	hasAudio, hasSubs := false, false
	audioFirst := true
	for _, r := range m.rends {
		switch r.Kind {
		case KindAudio:
			hasAudio = true
			def := "NO"
			if audioFirst {
				def = "YES"
				audioFirst = false
			}
			fmt.Fprintf(&b,
				"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=%q,LANGUAGE=%q,DEFAULT=%s,AUTOSELECT=YES,URI=%q\n",
				r.Name, r.Lang, def, r.PlaylistURI())
		case KindSubs:
			hasSubs = true
			fmt.Fprintf(&b,
				"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=%q,LANGUAGE=%q,DEFAULT=NO,AUTOSELECT=YES,URI=%q\n",
				r.Name, r.Lang, r.PlaylistURI())
		}
	}
	bandwidth := m.video.BitrateK*1000 + 660000
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.640028,mp4a.40.2\"",
		bandwidth, m.video.Width, m.video.Height)
	if hasAudio {
		b.WriteString(",AUDIO=\"audio\"")
	}
	if hasSubs {
		b.WriteString(",SUBTITLES=\"subs\"")
	}
	b.WriteString("\nv.m3u8\n")
	return b.String()
}
