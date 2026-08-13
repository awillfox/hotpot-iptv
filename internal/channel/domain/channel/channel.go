package channel

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"hotpot-iptv/sqlc"
)

var ErrNotFound = errors.New("channel not found")

type Channel struct {
	ID            int32  `json:"id"`
	Name          string `json:"name"`
	Number        int32  `json:"number"`
	Slug          string `json:"slug"`
	Enabled       bool   `json:"enabled"`
	VideoWidth    int32  `json:"video_width"`
	VideoHeight   int32  `json:"video_height"`
	VideoBitrateK int32  `json:"video_bitrate_k"`
	// SourceFolder empty means the playlist is hand-picked; set means it is
	// derived by walking this folder under the media root.
	SourceFolder string    `json:"source_folder"`
	CreatedAt    time.Time `json:"created_at"`
}

type PlaylistItem struct {
	ID       int32  `json:"id"`
	Position int32  `json:"position"`
	Path     string `json:"path"`
}

func NewFromSQLC(sq sqlc.Channel) Channel {
	return Channel{
		ID: sq.ID, Name: sq.Name, Number: sq.Number, Slug: sq.Slug,
		Enabled: sq.Enabled, VideoWidth: sq.VideoWidth, VideoHeight: sq.VideoHeight,
		VideoBitrateK: sq.VideoBitrateK, SourceFolder: sq.SourceFolder.String,
		CreatedAt: sq.CreatedAt.Time,
	}
}

func ItemFromSQLC(sq sqlc.PlaylistItem) PlaylistItem {
	return PlaylistItem{ID: sq.ID, Position: sq.Position, Path: sq.Path}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(s, "-")
}
