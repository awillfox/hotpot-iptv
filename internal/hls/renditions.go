package hls

import (
	"fmt"

	"hotpot-iptv/internal/probe"
)

type RenditionKind string

const (
	KindVideo RenditionKind = "video"
	KindAudio RenditionKind = "audio"
	KindSubs  RenditionKind = "subs"
)

type Rendition struct {
	Kind RenditionKind
	Key  string
	Lang string
	Name string
}

func (r Rendition) PlaylistURI() string { return r.Key + ".m3u8" }

var langNames = map[string]string{
	"tha": "Thai", "eng": "English", "jpn": "Japanese", "kor": "Korean",
	"chi": "Chinese", "zho": "Chinese", "spa": "Spanish", "fre": "French",
	"fra": "French", "ger": "German", "deu": "German", "ita": "Italian",
	"por": "Portuguese", "rus": "Russian", "vie": "Vietnamese", "ind": "Indonesian",
	"may": "Malay", "hin": "Hindi", "ara": "Arabic", "und": "Unknown",
}

func LangName(code string) string {
	if n, ok := langNames[code]; ok {
		return n
	}
	return code
}

// key builds "a_eng_0" style keys: kind prefix, language, occurrence ordinal
// of that language within a single file.
func key(prefix, lang string, occ int) string {
	return fmt.Sprintf("%s_%s_%d", prefix, lang, occ)
}

func displayName(lang string, occ int) string {
	if occ == 0 {
		return LangName(lang)
	}
	return fmt.Sprintf("%s %d", LangName(lang), occ+1)
}

// ComputeRenditions unions tracks across all playlist items so the channel's
// rendition set stays fixed for the whole session.
func ComputeRenditions(probes []probe.Result) []Rendition {
	rends := []Rendition{{Kind: KindVideo, Key: "v", Name: "Video"}}
	seen := map[string]bool{}
	// audio first (order of first appearance), then subs
	for _, p := range probes {
		occ := map[string]int{}
		for _, a := range p.Audio {
			k := key("a", a.Lang, occ[a.Lang])
			occ[a.Lang]++
			if !seen[k] {
				seen[k] = true
				rends = append(rends, Rendition{
					Kind: KindAudio, Key: k, Lang: a.Lang,
					Name: displayName(a.Lang, countLang(rends, KindAudio, a.Lang)),
				})
			}
		}
	}
	for _, p := range probes {
		occ := map[string]int{}
		for _, s := range p.Subs {
			k := key("s", s.Lang, occ[s.Lang])
			occ[s.Lang]++
			if !seen[k] {
				seen[k] = true
				rends = append(rends, Rendition{
					Kind: KindSubs, Key: k, Lang: s.Lang,
					Name: displayName(s.Lang, countLang(rends, KindSubs, s.Lang)),
				})
			}
		}
	}
	return rends
}

func countLang(rends []Rendition, kind RenditionKind, lang string) int {
	n := 0
	for _, r := range rends {
		if r.Kind == kind && r.Lang == lang {
			n++
		}
	}
	return n
}

// MapTracks maps each rendition key to the matching track index in this file's
// probe (nth track of that language), or -1 when the file lacks it.
func MapTracks(rends []Rendition, p probe.Result) map[string]int {
	m := make(map[string]int, len(rends))
	for _, r := range rends {
		switch r.Kind {
		case KindVideo:
			m[r.Key] = 0
		case KindAudio:
			m[r.Key] = -1
			occ := map[string]int{}
			for i, a := range p.Audio {
				if key("a", a.Lang, occ[a.Lang]) == r.Key {
					m[r.Key] = i
					break
				}
				occ[a.Lang]++
			}
		case KindSubs:
			m[r.Key] = -1
			occ := map[string]int{}
			for i, s := range p.Subs {
				if key("s", s.Lang, occ[s.Lang]) == r.Key {
					m[r.Key] = i
					break
				}
				occ[s.Lang]++
			}
		}
	}
	return m
}
