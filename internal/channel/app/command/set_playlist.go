package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/sqlc"
)

type Prober interface {
	Probe(ctx context.Context, absPath string) (ffmpeg.ProbeResult, error)
}

type SetPlaylistHandler struct {
	pool      *pgxpool.Pool
	queries   *sqlc.Queries
	prober    Prober
	mediaPath string
}

func NewSetPlaylistHandler(pool *pgxpool.Pool, q *sqlc.Queries, p Prober, mediaPath string) SetPlaylistHandler {
	return SetPlaylistHandler{pool: pool, queries: q, prober: p, mediaPath: mediaPath}
}

type SetPlaylistInput struct {
	ChannelID int32
	Paths     []string // relative to media root
}

func (h SetPlaylistHandler) Handle(ctx context.Context, in SetPlaylistInput) ([]channel.PlaylistItem, error) {
	for _, rel := range in.Paths {
		abs, err := h.resolve(rel)
		if err != nil {
			return nil, err
		}
		if err := h.ensureProbed(ctx, rel, abs); err != nil {
			return nil, err
		}
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := h.queries.WithTx(tx)

	if err := q.DeletePlaylistItems(ctx, in.ChannelID); err != nil {
		return nil, fmt.Errorf("clear playlist: %w", err)
	}
	positions := make([]int32, len(in.Paths))
	for i := range in.Paths {
		positions[i] = int32(i)
	}
	var items []channel.PlaylistItem
	if len(in.Paths) > 0 {
		rows, err := q.InsertPlaylistItems(ctx, sqlc.InsertPlaylistItemsParams{
			ChannelID: in.ChannelID, Positions: positions, Paths: in.Paths,
		})
		if err != nil {
			return nil, fmt.Errorf("insert playlist items: %w", err)
		}
		items = make([]channel.PlaylistItem, 0, len(rows))
		for _, r := range rows {
			items = append(items, channel.ItemFromSQLC(r))
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit playlist: %w", err)
	}
	return items, nil
}

func (h SetPlaylistHandler) resolve(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	return filepath.Join(h.mediaPath, clean), nil
}

// ensureProbed re-probes only when the file is new or size/mtime changed.
func (h SetPlaylistHandler) ensureProbed(ctx context.Context, rel, abs string) error {
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("media file %q: %w", rel, err)
	}
	existing, err := h.queries.GetMediaFile(ctx, rel)
	if err == nil && existing.Size == st.Size() && existing.Mtime.Time.Unix() == st.ModTime().Unix() {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get media file: %w", err)
	}
	probe, err := h.prober.Probe(ctx, abs)
	if err != nil {
		return fmt.Errorf("probe %q: %w", rel, err)
	}
	raw, err := json.Marshal(probe)
	if err != nil {
		return fmt.Errorf("marshal probe: %w", err)
	}
	_, err = h.queries.UpsertMediaFile(ctx, sqlc.UpsertMediaFileParams{
		Path: rel, Size: st.Size(),
		Mtime:      pgtype.Timestamptz{Time: st.ModTime(), Valid: true},
		DurationMs: probe.DurationMs, VideoCodec: probe.VideoCodec, Probe: raw,
	})
	if err != nil {
		return fmt.Errorf("upsert media file: %w", err)
	}
	return nil
}
