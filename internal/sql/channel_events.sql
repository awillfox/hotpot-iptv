-- name: InsertChannelEvent :exec
INSERT INTO channel_events (channel_id, level, message) VALUES ($1, $2, $3);

-- name: ListChannelEvents :many
SELECT * FROM channel_events WHERE channel_id = $1 ORDER BY id DESC LIMIT $2;
