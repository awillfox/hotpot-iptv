// Package probe holds the media-probe result types shared between internal/ffmpeg
// (which produces them) and internal/hls (which consumes them to compute
// renditions and track maps). It exists to avoid an import cycle: ffmpeg also
// needs hls.Rendition (for command building), so the probe result types can't
// live in either package importing the other.
package probe

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

type Result struct {
	DurationMs int64           `json:"duration_ms"`
	VideoCodec string          `json:"video_codec"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	Audio      []AudioTrack    `json:"audio"`
	Subs       []SubtitleTrack `json:"subs"`
}
