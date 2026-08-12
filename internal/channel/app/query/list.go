package query

import (
	"context"
	"fmt"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type ListHandler struct {
	queries *sqlc.Queries
}

func NewListHandler(q *sqlc.Queries) ListHandler { return ListHandler{queries: q} }

func (h ListHandler) Handle(ctx context.Context) ([]channel.Channel, error) {
	rows, err := h.queries.ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	out := make([]channel.Channel, 0, len(rows))
	for _, r := range rows {
		out = append(out, channel.NewFromSQLC(r))
	}
	return out, nil
}
