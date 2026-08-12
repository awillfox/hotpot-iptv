-- name: UpsertMediaFile :one
INSERT INTO media_files (path, size, mtime, duration_ms, video_codec, probe, probed_at)
VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (path) DO UPDATE
SET size = EXCLUDED.size, mtime = EXCLUDED.mtime, duration_ms = EXCLUDED.duration_ms,
    video_codec = EXCLUDED.video_codec, probe = EXCLUDED.probe, probed_at = now()
RETURNING *;

-- name: GetMediaFile :one
SELECT * FROM media_files WHERE path = $1;

-- name: GetMediaFilesByPaths :many
SELECT * FROM media_files WHERE path = ANY(sqlc.arg(paths)::text[]);
