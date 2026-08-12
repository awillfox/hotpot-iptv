package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type AudioTrack struct {
	Index    int    `json:"index"`
	Lang     string `json:"lang"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
}

type SubtitleTrack struct {
	Index    int    `json:"index"`
	Lang     string `json:"lang"`
	Codec    string `json:"codec"`
	External bool   `json:"external"`
	Path     string `json:"path,omitempty"`
}

type ProbeResult struct {
	DurationMs int64           `json:"duration_ms"`
	VideoCodec string          `json:"video_codec"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	Audio      []AudioTrack    `json:"audio"`
	Subs       []SubtitleTrack `json:"subs"`
}

type CLI struct {
	FFprobePath string
}

var textSubCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true,
	"mov_text": true, "webvtt": true, "text": true,
}

func (c CLI) Probe(ctx context.Context, absPath string) (ProbeResult, error) {
	out, err := exec.CommandContext(ctx, c.FFprobePath,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", absPath,
	).Output()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe %s: %w", absPath, err)
	}
	p, err := parseProbeOutput(out)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("parse ffprobe output for %s: %w", absPath, err)
	}
	p.Subs = append(p.Subs, findExternalSubs(absPath)...)
	return p, nil
}

type rawProbe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
		Tags      struct {
			Language string `json:"language"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func parseProbeOutput(raw []byte) (ProbeResult, error) {
	var rp rawProbe
	if err := json.Unmarshal(raw, &rp); err != nil {
		return ProbeResult{}, err
	}
	var p ProbeResult
	if rp.Format.Duration != "" {
		sec, err := strconv.ParseFloat(rp.Format.Duration, 64)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("bad duration %q: %w", rp.Format.Duration, err)
		}
		p.DurationMs = int64(sec * 1000)
	}
	lang := func(l string) string {
		if l == "" {
			return "und"
		}
		return strings.ToLower(l)
	}
	for _, s := range rp.Streams {
		switch s.CodecType {
		case "video":
			if p.VideoCodec == "" { // first video stream wins
				p.VideoCodec = s.CodecName
				p.Width, p.Height = s.Width, s.Height
			}
		case "audio":
			p.Audio = append(p.Audio, AudioTrack{
				Index: len(p.Audio), Lang: lang(s.Tags.Language),
				Codec: s.CodecName, Channels: s.Channels,
			})
		case "subtitle":
			if textSubCodecs[s.CodecName] {
				p.Subs = append(p.Subs, SubtitleTrack{
					Index: len(p.Subs), Lang: lang(s.Tags.Language), Codec: s.CodecName,
				})
			}
		}
	}
	if p.VideoCodec == "" {
		return ProbeResult{}, fmt.Errorf("no video stream")
	}
	if p.DurationMs <= 0 {
		return ProbeResult{}, fmt.Errorf("no duration")
	}
	return p, nil
}

// findExternalSubs picks up sibling files: <base>.srt (lang und) and
// <base>.<lang>.srt. Results sorted by filename for determinism.
func findExternalSubs(videoAbsPath string) []SubtitleTrack {
	dir := filepath.Dir(videoAbsPath)
	base := strings.TrimSuffix(filepath.Base(videoAbsPath), filepath.Ext(videoAbsPath))
	matches, _ := filepath.Glob(filepath.Join(dir, base+"*.srt"))
	sort.Strings(matches)
	var subs []SubtitleTrack
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".srt")
		if name != base && !strings.HasPrefix(name, base+".") {
			continue // e.g. "movie2.srt" for base "movie"
		}
		l := "und"
		if rest := strings.TrimPrefix(name, base+"."); rest != name && rest != "" && len(rest) <= 3 {
			l = strings.ToLower(rest)
		}
		subs = append(subs, SubtitleTrack{Index: -1, Lang: l, Codec: "srt", External: true, Path: m})
	}
	return subs
}
