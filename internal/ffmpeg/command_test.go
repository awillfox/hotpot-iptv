package ffmpeg

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"hotpot-iptv/internal/hls"
)

func spec() EncodeSpec {
	return EncodeSpec{
		InputPath:  "/media/movie.mkv",
		OutDir:     "/streams/ch/000001",
		SegmentSec: 4,
		DurationMs: 61500,
		Video:      VideoSettings{Width: 1920, Height: 1080, BitrateK: 5000, Encoder: "software"},
		Renditions: []hls.Rendition{
			{Kind: hls.KindVideo, Key: "v"},
			{Kind: hls.KindAudio, Key: "a_tha_0", Lang: "tha"},
			{Kind: hls.KindAudio, Key: "a_eng_0", Lang: "eng"},
			{Kind: hls.KindSubs, Key: "s_tha_0", Lang: "tha"},
		},
		TrackMap: map[string]int{"v": 0, "a_tha_0": 0, "a_eng_0": -1, "s_tha_0": 0},
	}
}

func TestBuildEncodeArgsSoftware(t *testing.T) {
	got := strings.Join(BuildEncodeArgs(spec()), " ")
	want := "-hide_banner -nostdin -loglevel error -progress pipe:1 " +
		"-re -i /media/movie.mkv " +
		"-f lavfi -t 61.500 -i anullsrc=r=48000:cl=stereo " +
		"-filter_complex [0:v:0]scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1[vout] " +
		"-map [vout] -c:v libx264 -preset veryfast -b:v 5000k -maxrate 6000k -bufsize 10000k -sc_threshold 0 " +
		"-force_key_frames expr:gte(t,n_forced*4) " +
		"-f segment -segment_time 4 -segment_format mpegts -output_ts_offset 10 " +
		"-segment_list /streams/ch/000001/v.csv -segment_list_type csv /streams/ch/000001/v_%d.ts " +
		"-map 0:a:0 -c:a aac -b:a 160k -ac 2 " +
		"-f segment -segment_time 4 -segment_format mpegts -output_ts_offset 10 " +
		"-segment_list /streams/ch/000001/a_tha_0.csv -segment_list_type csv /streams/ch/000001/a_tha_0_%d.ts " +
		"-map 1:a:0 -c:a aac -b:a 160k -ac 2 " +
		"-f segment -segment_time 4 -segment_format mpegts -output_ts_offset 10 " +
		"-segment_list /streams/ch/000001/a_eng_0.csv -segment_list_type csv /streams/ch/000001/a_eng_0_%d.ts"
	assert.Equal(t, want, got)
}

func TestBuildEncodeArgsNvenc(t *testing.T) {
	s := spec()
	s.Video.Encoder = "nvenc"
	got := strings.Join(BuildEncodeArgs(s), " ")
	assert.Contains(t, got, "-c:v h264_nvenc -preset p5 -rc vbr -b:v 5000k -maxrate 6000k -bufsize 10000k -profile:v high -forced-idr 1")
	assert.NotContains(t, got, "libx264")
}

func TestBuildEncodeArgsNoFillerInput(t *testing.T) {
	s := spec()
	s.TrackMap["a_eng_0"] = 1 // both real → no anullsrc input
	got := strings.Join(BuildEncodeArgs(s), " ")
	assert.NotContains(t, got, "anullsrc")
	assert.Contains(t, got, "-map 0:a:1")
}

func TestBuildSubExtractArgs(t *testing.T) {
	embedded := strings.Join(BuildSubExtractArgs("/media/movie.mkv",
		SubtitleTrack{Index: 2}, "/streams/ch/000001/s_tha_0.vtt"), " ")
	assert.Equal(t,
		"-hide_banner -nostdin -loglevel error -y -i /media/movie.mkv -map 0:s:2 -f webvtt /streams/ch/000001/s_tha_0.vtt",
		embedded)

	external := strings.Join(BuildSubExtractArgs("/media/movie.mkv",
		SubtitleTrack{Index: -1, External: true, Path: "/media/movie.eng.srt"},
		"/streams/ch/000001/s_eng_0.vtt"), " ")
	assert.Equal(t,
		"-hide_banner -nostdin -loglevel error -y -i /media/movie.eng.srt -map 0:s:0 -f webvtt /streams/ch/000001/s_eng_0.vtt",
		external)
}
