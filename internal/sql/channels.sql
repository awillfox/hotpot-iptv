-- name: CreateChannel :one
INSERT INTO channels (name, number, slug, video_width, video_height, video_bitrate_k)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListChannels :many
SELECT * FROM channels WHERE deleted_at IS NULL ORDER BY number;

-- name: GetChannel :one
SELECT * FROM channels WHERE id = $1 AND deleted_at IS NULL;

-- name: GetChannelBySlug :one
SELECT * FROM channels WHERE slug = $1 AND deleted_at IS NULL;

-- name: UpdateChannel :one
UPDATE channels
SET name = $2, number = $3, slug = $4, enabled = $5,
    video_width = $6, video_height = $7, video_bitrate_k = $8
WHERE id = $1 AND deleted_at IS NULL
RETURNING *;

-- name: SoftDeleteChannel :exec
UPDATE channels SET deleted_at = now() WHERE id = $1;
