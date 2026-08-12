package command

import (
	"context"
	"fmt"

	"hotpot-iptv/sqlc"
)

type DeleteHandler struct {
	queries *sqlc.Queries
}

func NewDeleteHandler(q *sqlc.Queries) DeleteHandler { return DeleteHandler{queries: q} }

func (h DeleteHandler) Handle(ctx context.Context, id int32) error {
	if err := h.queries.SoftDeleteChannel(ctx, id); err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	return nil
}
