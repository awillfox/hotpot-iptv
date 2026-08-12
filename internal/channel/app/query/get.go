package query

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type GetHandler struct {
	queries *sqlc.Queries
}

func NewGetHandler(q *sqlc.Queries) GetHandler { return GetHandler{queries: q} }

func (h GetHandler) Handle(ctx context.Context, id int32) (channel.Channel, error) {
	sq, err := h.queries.GetChannel(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.Channel{}, channel.ErrNotFound
	}
	if err != nil {
		return channel.Channel{}, fmt.Errorf("get channel: %w", err)
	}
	return channel.NewFromSQLC(sq), nil
}
