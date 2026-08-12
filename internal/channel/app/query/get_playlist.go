package query

import (
	"context"
	"fmt"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type GetPlaylistHandler struct {
	queries *sqlc.Queries
}

func NewGetPlaylistHandler(q *sqlc.Queries) GetPlaylistHandler { return GetPlaylistHandler{queries: q} }

func (h GetPlaylistHandler) Handle(ctx context.Context, channelID int32) ([]channel.PlaylistItem, error) {
	rows, err := h.queries.ListPlaylistItems(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}
	out := make([]channel.PlaylistItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, channel.ItemFromSQLC(r))
	}
	return out, nil
}
