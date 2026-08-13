package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type UpdateHandler struct {
	queries *sqlc.Queries
}

func NewUpdateHandler(q *sqlc.Queries) UpdateHandler { return UpdateHandler{queries: q} }

type UpdateInput struct {
	ID            int32
	Name          string
	Number        int32
	Slug          string
	Enabled       bool
	VideoWidth    int32
	VideoHeight   int32
	VideoBitrateK int32
	SourceFolder  string
}

func (h UpdateHandler) Handle(ctx context.Context, in UpdateInput) (channel.Channel, error) {
	if in.Slug == "" {
		in.Slug = channel.Slugify(in.Name)
	}
	sq, err := h.queries.UpdateChannel(ctx, sqlc.UpdateChannelParams{
		ID: in.ID, Name: in.Name, Number: in.Number, Slug: in.Slug, Enabled: in.Enabled,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
		SourceFolder: pgtype.Text{String: in.SourceFolder, Valid: in.SourceFolder != ""},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.Channel{}, channel.ErrNotFound
	}
	if err != nil {
		return channel.Channel{}, fmt.Errorf("update channel: %w", err)
	}
	return channel.NewFromSQLC(sq), nil
}
