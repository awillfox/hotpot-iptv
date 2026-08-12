package command

import (
	"context"
	"fmt"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type CreateHandler struct {
	queries *sqlc.Queries
}

func NewCreateHandler(q *sqlc.Queries) CreateHandler { return CreateHandler{queries: q} }

type CreateInput struct {
	Name          string
	Number        int32
	Slug          string
	VideoWidth    int32
	VideoHeight   int32
	VideoBitrateK int32
}

func (h CreateHandler) Handle(ctx context.Context, in CreateInput) (channel.Channel, error) {
	if in.Slug == "" {
		in.Slug = channel.Slugify(in.Name)
	}
	if in.VideoWidth == 0 {
		in.VideoWidth = 1920
	}
	if in.VideoHeight == 0 {
		in.VideoHeight = 1080
	}
	if in.VideoBitrateK == 0 {
		in.VideoBitrateK = 5000
	}
	sq, err := h.queries.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: in.Name, Number: in.Number, Slug: in.Slug,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
	if err != nil {
		return channel.Channel{}, fmt.Errorf("create channel: %w", err)
	}
	return channel.NewFromSQLC(sq), nil
}
