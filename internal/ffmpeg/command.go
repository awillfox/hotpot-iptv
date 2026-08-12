package ffmpeg

import (
	"fmt"
	"path/filepath"

	"hotpot-iptv/internal/hls"
)

type VideoSettings struct {
	Width    int
	Height   int
	BitrateK int
	Encoder  string // "nvenc" | "software"
}

type EncodeSpec struct {
	InputPath  string
	OutDir     string
	SegmentSec int
	DurationMs int64
	Video      VideoSettings
	Renditions []hls.Rendition
	TrackMap   map[string]int
}

// BuildEncodeArgs produces one ffmpeg invocation that encodes the video
// rendition plus every audio rendition. Audio renditions the file lacks
// (TrackMap value -1) are fed from a silent anullsrc input so their segment
// timeline stays continuous. Subtitles are handled separately
// (BuildSubExtractArgs) because they are extracted before the encode starts.
func BuildEncodeArgs(s EncodeSpec) []string {
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-progress", "pipe:1",
		"-re", "-i", s.InputPath,
	}

	needSilence := false
	for _, r := range s.Renditions {
		if r.Kind == hls.KindAudio && s.TrackMap[r.Key] == -1 {
			needSilence = true
		}
	}
	if needSilence {
		args = append(args,
			"-f", "lavfi",
			"-t", fmt.Sprintf("%.3f", float64(s.DurationMs)/1000),
			"-i", "anullsrc=r=48000:cl=stereo",
		)
	}

	args = append(args, "-filter_complex", fmt.Sprintf(
		"[0:v:0]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1[vout]",
		s.Video.Width, s.Video.Height, s.Video.Width, s.Video.Height))

	// video output
	args = append(args, "-map", "[vout]")
	args = append(args, videoCodecArgs(s.Video)...)
	args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", s.SegmentSec))
	args = append(args, segmentArgs(s.OutDir, "v", s.SegmentSec)...)

	// audio outputs, rendition order
	for _, r := range s.Renditions {
		if r.Kind != hls.KindAudio {
			continue
		}
		idx := s.TrackMap[r.Key]
		if idx >= 0 {
			args = append(args, "-map", fmt.Sprintf("0:a:%d", idx))
		} else {
			args = append(args, "-map", "1:a:0")
		}
		args = append(args, "-c:a", "aac", "-b:a", "160k", "-ac", "2")
		args = append(args, segmentArgs(s.OutDir, r.Key, s.SegmentSec)...)
	}
	return args
}

func videoCodecArgs(v VideoSettings) []string {
	b := v.BitrateK
	common := []string{
		"-b:v", fmt.Sprintf("%dk", b),
		"-maxrate", fmt.Sprintf("%dk", b*12/10),
		"-bufsize", fmt.Sprintf("%dk", b*2),
	}
	if v.Encoder == "nvenc" {
		return append([]string{"-c:v", "h264_nvenc", "-preset", "p5", "-rc", "vbr"},
			append(common, "-profile:v", "high", "-forced-idr", "1")...)
	}
	return append([]string{"-c:v", "libx264", "-preset", "veryfast"},
		append(common, "-sc_threshold", "0")...)
}

func segmentArgs(outDir, key string, segSec int) []string {
	return []string{
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%d", segSec),
		"-segment_format", "mpegts",
		"-output_ts_offset", "10",
		"-segment_list", filepath.Join(outDir, key+".csv"),
		"-segment_list_type", "csv",
		filepath.Join(outDir, key+"_%d.ts"),
	}
}

// BuildSubExtractArgs extracts one subtitle track to a whole-file WebVTT.
func BuildSubExtractArgs(inputPath string, track SubtitleTrack, outPath string) []string {
	in := inputPath
	mapArg := fmt.Sprintf("0:s:%d", track.Index)
	if track.External {
		in = track.Path
		mapArg = "0:s:0"
	}
	return []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", in, "-map", mapArg, "-f", "webvtt", outPath,
	}
}
