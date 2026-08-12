-- name: UpsertChannelState :one
INSERT INTO channel_state (channel_id, item_position, item_started_at, status, last_error, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (channel_id) DO UPDATE
SET item_position = EXCLUDED.item_position, item_started_at = EXCLUDED.item_started_at,
    status = EXCLUDED.status, last_error = EXCLUDED.last_error, updated_at = now()
RETURNING *;

-- name: GetChannelState :one
SELECT * FROM channel_state WHERE channel_id = $1;

-- name: ListRunningChannelStates :many
SELECT * FROM channel_state WHERE status = 'running';
