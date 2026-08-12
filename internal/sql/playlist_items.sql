-- name: DeletePlaylistItems :exec
DELETE FROM playlist_items WHERE channel_id = $1;

-- name: InsertPlaylistItems :many
INSERT INTO playlist_items (channel_id, position, path)
SELECT sqlc.arg(channel_id), unnest(sqlc.arg(positions)::int[]), unnest(sqlc.arg(paths)::text[])
RETURNING *;

-- name: ListPlaylistItems :many
SELECT * FROM playlist_items WHERE channel_id = $1 ORDER BY position;
