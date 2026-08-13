package engine

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"hotpot-iptv/sqlc"
)

// SQLStore persists runner state and channel events. It implements Store.
type SQLStore struct {
	q *sqlc.Queries
}

func NewSQLStore(q *sqlc.Queries) *SQLStore { return &SQLStore{q: q} }

func (s *SQLStore) SaveState(ctx context.Context, channelID, itemPos int32, startedAt time.Time, status, lastErr string) error {
	// A zero startedAt means "not playing" and must land as NULL, not as the
	// zero time — the UI reads the null-ness to decide whether to show a clock.
	started := pgtype.Timestamptz{}
	if !startedAt.IsZero() {
		started = pgtype.Timestamptz{Time: startedAt, Valid: true}
	}
	lastErrVal := pgtype.Text{}
	if lastErr != "" {
		lastErrVal = pgtype.Text{String: lastErr, Valid: true}
	}
	_, err := s.q.UpsertChannelState(ctx, sqlc.UpsertChannelStateParams{
		ChannelID: channelID, ItemPosition: itemPos,
		ItemStartedAt: started, Status: status, LastError: lastErrVal,
	})
	return err
}

// LogEvent is best-effort: the runner calls it on paths where there is nothing
// useful to do with a logging failure, so it reports and moves on.
func (s *SQLStore) LogEvent(ctx context.Context, channelID int32, level, message string) {
	if err := s.q.InsertChannelEvent(ctx, sqlc.InsertChannelEventParams{
		ChannelID: channelID, Level: level, Message: message,
	}); err != nil {
		log.Printf("log event for channel %d: %v", channelID, err)
	}
}
