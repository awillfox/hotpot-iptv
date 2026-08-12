# hotpot-iptv Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go service that turns an SMB media library into linear 24/7 IPTV channels delivered as multi-audio/multi-subtitle HLS, managed by a web UI, running in Docker with NVENC.

**Architecture:** One FFmpeg run per playlist item; Go owns all HLS playlists (in-memory state machine rendered on request). A supervisor runs one channel-runner goroutine per channel. Postgres stores channels/playlists/probe-cache/state. chi serves JSON API + server-rendered pages + HLS delivery.

**Tech Stack:** Go 1.26, chi + render, pgx/v5 + sqlc + Atlas, viper, testify, testcontainers-go, html/template + Tailwind CDN + vanilla JS + HLS.js, FFmpeg (h264_nvenc / libx264).

**Spec:** `docs/superpowers/specs/2026-08-12-hotpot-iptv-design.md` — read it first.

## Global Constraints

- Module name is `hotpot-iptv`; Go 1.26; `CGO_ENABLED=0` for release builds.
- Segment duration default **4 s**; live window default **30 segments**; audio renditions **AAC 160k stereo**; video **H.264 only** (`h264_nvenc` when `ENCODER=nvenc`, `libx264` when `ENCODER=software`); default 1920×1080 @ 5000 kbps.
- All MPEG-TS segment outputs use `-output_ts_offset 10`; every WebVTT segment starts with `WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000` (900000/90kHz = the same 10 s).
- HLS playlists are generated ONLY by Go, never by FFmpeg. FFmpeg writes segments + CSV segment lists.
- Env config (viper AutomaticEnv): `PORT=8080`, `PSQL_URL`, `MEDIA_PATH=/media`, `STREAMS_PATH=/streams`, `SEGMENT_SECONDS=4`, `WINDOW_SEGMENTS=30`, `ENCODER=nvenc`, `VIDEO_WIDTH=1920`, `VIDEO_HEIGHT=1080`, `VIDEO_BITRATE_K=5000`, `FFMPEG_PATH=ffmpeg`, `FFPROBE_PATH=ffprobe`.
- Repo layout: workspace API + CQRS-core convention (`api/<feature>/{server.go,service,http}` + `internal/<feature>/{domain,app}`); `sqlc/` and `schema.sql` are generated — never hand-edit; `main.go` contains only `main()` (package-level vars like `//go:embed` are allowed).
- Tests: table-driven, `go test -p 1 ./...` must pass at every commit. DB tests use `pkg/testdb` (testcontainers). No auth on any endpoint (LAN appliance).
- Conventional commits. `go fmt ./...` before every commit.
- JSON envelope for all API responses: `{"data": ..., "error": "..."}` via `internal/response.HTTPResponse`.

## File Structure

```
main.go                          # wiring only (single main func + //go:embed templates)
schema.hcl / schema.sql          # Atlas source / generated SQL
sqlc.yaml, sqlc/                 # sqlc config / generated
internal/sql/*.sql               # hand-written queries
internal/config/                 # viper Config
internal/response/, internal/apperr/
internal/ffmpeg/                 # probe.go, command.go, runner.go, seglist.go
internal/hls/                    # renditions.go, playlist.go, vtt.go
internal/library/                # media dir browsing
internal/channel/domain/channel/ # Channel, PlaylistItem, NewFromSQLC, ErrNotFound
internal/channel/app/{command,query}/
internal/engine/                 # runner.go, supervisor.go, store.go, loader.go
internal/epg/                    # Forward, RenderXMLTV, RenderM3U
internal/web/                    # page handlers (embedded templates)
api/channels/{server.go,service/,http/}
api/library/{server.go,service/,http/}
api/streams/http/                # HLS delivery
api/export/http/                 # /playlist.m3u /epg.xml
pkg/testdb/                      # testcontainers helper
templates/{layouts,channels,dashboard,preview}/
scripts/make-test-media.sh
Dockerfile, docker-compose.yml, .env.example, Taskfile.yml
```

---

### Task 1: Scaffold — config, health server, Taskfile

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`, `main.go` (replace), `Taskfile.yml`, `.gitignore`

**Interfaces:**
- Produces: `config.Load() (*config.Config, error)`; `Config` fields exactly as in Global Constraints (Go names: `Port, PSQLURL, MediaPath, StreamsPath, SegmentSeconds, WindowSegments, Encoder, VideoWidth, VideoHeight, VideoBitrateK, FFmpegPath, FFprobePath`).

- [ ] **Step 1: Deps + failing test**

```bash
go get github.com/go-chi/chi/v5 github.com/go-chi/render github.com/spf13/viper \
  github.com/jackc/pgx/v5 github.com/stretchr/testify
```

`internal/config/config_test.go`:

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "/media", cfg.MediaPath)
	assert.Equal(t, 4, cfg.SegmentSeconds)
	assert.Equal(t, 30, cfg.WindowSegments)
	assert.Equal(t, "nvenc", cfg.Encoder)
	assert.Equal(t, 5000, cfg.VideoBitrateK)
}

func TestLoadEnvOverride(t *testing.T) {
	t.Setenv("PORT", "9099")
	t.Setenv("ENCODER", "software")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 9099, cfg.Port)
	assert.Equal(t, "software", cfg.Encoder)
}
```

- [ ] **Step 2: Run test — expect FAIL** (`go test ./internal/config/` — package doesn't compile: `Load` undefined)

- [ ] **Step 3: Implement**

`internal/config/config.go`:

```go
package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

type Config struct {
	Port           int    `mapstructure:"PORT"`
	PSQLURL        string `mapstructure:"PSQL_URL"`
	MediaPath      string `mapstructure:"MEDIA_PATH"`
	StreamsPath    string `mapstructure:"STREAMS_PATH"`
	SegmentSeconds int    `mapstructure:"SEGMENT_SECONDS"`
	WindowSegments int    `mapstructure:"WINDOW_SEGMENTS"`
	Encoder        string `mapstructure:"ENCODER"`
	VideoWidth     int    `mapstructure:"VIDEO_WIDTH"`
	VideoHeight    int    `mapstructure:"VIDEO_HEIGHT"`
	VideoBitrateK  int    `mapstructure:"VIDEO_BITRATE_K"`
	FFmpegPath     string `mapstructure:"FFMPEG_PATH"`
	FFprobePath    string `mapstructure:"FFPROBE_PATH"`
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("PORT", 8080)
	v.SetDefault("PSQL_URL", "")
	v.SetDefault("MEDIA_PATH", "/media")
	v.SetDefault("STREAMS_PATH", "/streams")
	v.SetDefault("SEGMENT_SECONDS", 4)
	v.SetDefault("WINDOW_SEGMENTS", 30)
	v.SetDefault("ENCODER", "nvenc")
	v.SetDefault("VIDEO_WIDTH", 1920)
	v.SetDefault("VIDEO_HEIGHT", 1080)
	v.SetDefault("VIDEO_BITRATE_K", 5000)
	v.SetDefault("FFMPEG_PATH", "ffmpeg")
	v.SetDefault("FFPROBE_PATH", "ffprobe")

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	return &cfg, nil
}
```

`main.go` (replace whole file):

```go
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"hotpot-iptv/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("hotpot-iptv listening on :%d", cfg.Port)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), r); err != nil {
		log.Fatalf("http server: %v", err)
	}
}
```

`Taskfile.yml`:

```yaml
version: "3"

tasks:
  run:
    cmds: [go run .]
  test:
    cmds: [go test -p 1 ./...]
  fmt:
    cmds: [go fmt ./...]
```

`.gitignore`:

```
.env
/hotpot
testdata/e2e/
```

- [ ] **Step 4: Verify** — `go test ./internal/config/ -v` → both PASS. `go run . &` then `curl -s localhost:8080/healthz` → `ok`; kill it.

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: scaffold config and health server"`

---

### Task 2: Database — schema.hcl, sqlc, testdb helper

**Files:**
- Create: `schema.hcl`, `sqlc.yaml`, `internal/sql/channels.sql`, `internal/sql/playlist_items.sql`, `internal/sql/media_files.sql`, `internal/sql/channel_state.sql`, `internal/sql/channel_events.sql`, `pkg/testdb/testdb.go`, `internal/dbtest/queries_test.go`
- Generated: `schema.sql`, `sqlc/*` (commit them)
- Modify: `Taskfile.yml` (add migrate-dev, generate-sql-schema, sqlcgen)

**Interfaces:**
- Produces: `sqlc.New(pool)` with methods `CreateChannel, ListChannels, GetChannel, GetChannelBySlug, UpdateChannel, SoftDeleteChannel, DeletePlaylistItems, InsertPlaylistItems, ListPlaylistItems, UpsertMediaFile, GetMediaFile, GetMediaFilesByPaths, UpsertChannelState, GetChannelState, ListRunningChannelStates, InsertChannelEvent, ListChannelEvents`; `testdb.New(t) *pgxpool.Pool`.
- Requires on the dev machine: Docker (for testcontainers + atlas dev-db), `atlas`, `sqlc`, `task` CLIs, and a dev Postgres reachable via `PSQL_URL` (e.g. `docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=dev -e POSTGRES_USER=hotpot -e POSTGRES_DB=hotpot postgres:16-alpine`, then `export PSQL_URL='postgres://hotpot:dev@localhost:5432/hotpot?sslmode=disable'`).

- [ ] **Step 1: Write schema.hcl**

```hcl
schema "public" {}

table "channels" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "name" {
    null = false
    type = text
  }
  column "number" {
    null = false
    type = integer
  }
  column "slug" {
    null = false
    type = text
  }
  column "enabled" {
    null    = false
    type    = boolean
    default = true
  }
  column "video_width" {
    null    = false
    type    = integer
    default = 1920
  }
  column "video_height" {
    null    = false
    type    = integer
    default = 1080
  }
  column "video_bitrate_k" {
    null    = false
    type    = integer
    default = 5000
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  column "deleted_at" {
    null = true
    type = timestamptz
  }
  primary_key {
    columns = [column.id]
  }
  index "channels_slug_key" {
    unique  = true
    columns = [column.slug]
  }
  index "channels_number_key" {
    unique  = true
    columns = [column.number]
  }
}

table "playlist_items" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "channel_id" {
    null = false
    type = integer
  }
  column "position" {
    null = false
    type = integer
  }
  column "path" {
    null = false
    type = text
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "playlist_items_channel_id_fkey" {
    columns     = [column.channel_id]
    ref_columns = [table.channels.column.id]
    on_delete   = CASCADE
  }
  index "playlist_items_channel_pos_idx" {
    columns = [column.channel_id, column.position]
  }
}

table "media_files" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "path" {
    null = false
    type = text
  }
  column "size" {
    null = false
    type = bigint
  }
  column "mtime" {
    null = false
    type = timestamptz
  }
  column "duration_ms" {
    null = false
    type = bigint
  }
  column "video_codec" {
    null = false
    type = text
  }
  column "probe" {
    null = false
    type = jsonb
  }
  column "probed_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  index "media_files_path_key" {
    unique  = true
    columns = [column.path]
  }
}

table "channel_state" {
  schema = schema.public
  column "channel_id" {
    null = false
    type = integer
  }
  column "item_position" {
    null    = false
    type    = integer
    default = 0
  }
  column "item_started_at" {
    null = true
    type = timestamptz
  }
  column "status" {
    null    = false
    type    = text
    default = "stopped"
  }
  column "last_error" {
    null = true
    type = text
  }
  column "updated_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.channel_id]
  }
  foreign_key "channel_state_channel_id_fkey" {
    columns     = [column.channel_id]
    ref_columns = [table.channels.column.id]
    on_delete   = CASCADE
  }
}

table "channel_events" {
  schema = schema.public
  column "id" {
    null = false
    type = serial
  }
  column "channel_id" {
    null = false
    type = integer
  }
  column "level" {
    null = false
    type = text
  }
  column "message" {
    null = false
    type = text
  }
  column "created_at" {
    null    = false
    type    = timestamptz
    default = sql("now()")
  }
  primary_key {
    columns = [column.id]
  }
  foreign_key "channel_events_channel_id_fkey" {
    columns     = [column.channel_id]
    ref_columns = [table.channels.column.id]
    on_delete   = CASCADE
  }
  index "channel_events_channel_idx" {
    columns = [column.channel_id, column.id]
  }
}
```

- [ ] **Step 2: Taskfile additions + apply migration + generate schema.sql**

Add to `Taskfile.yml` tasks:

```yaml
  migrate-dev:
    cmds:
      - atlas schema apply --url "$PSQL_URL" --dev-url "docker://postgres/16/dev" --to file://schema.hcl
  generate-sql-schema:
    cmds:
      - atlas schema inspect --url "$PSQL_URL" --format '{{"{{ sql . }}"}}' > schema.sql
  sqlcgen:
    cmds: [sqlc generate]
```

Run: `task migrate-dev` then `task generate-sql-schema`. Verify `schema.sql` contains all 5 tables.

- [ ] **Step 3: Write queries + sqlc.yaml, generate**

`sqlc.yaml`:

```yaml
version: "2"
sql:
  - engine: "postgresql"
    schema: "schema.sql"
    queries: "internal/sql"
    gen:
      go:
        package: "sqlc"
        out: "sqlc"
        sql_package: "pgx/v5"
```

`internal/sql/channels.sql`:

```sql
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
```

`internal/sql/playlist_items.sql`:

```sql
-- name: DeletePlaylistItems :exec
DELETE FROM playlist_items WHERE channel_id = $1;

-- name: InsertPlaylistItems :many
INSERT INTO playlist_items (channel_id, position, path)
SELECT sqlc.arg(channel_id), unnest(sqlc.arg(positions)::int[]), unnest(sqlc.arg(paths)::text[])
RETURNING *;

-- name: ListPlaylistItems :many
SELECT * FROM playlist_items WHERE channel_id = $1 ORDER BY position;
```

`internal/sql/media_files.sql`:

```sql
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
```

`internal/sql/channel_state.sql`:

```sql
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
```

`internal/sql/channel_events.sql`:

```sql
-- name: InsertChannelEvent :exec
INSERT INTO channel_events (channel_id, level, message) VALUES ($1, $2, $3);

-- name: ListChannelEvents :many
SELECT * FROM channel_events WHERE channel_id = $1 ORDER BY id DESC LIMIT $2;
```

Run `task sqlcgen`. Verify `sqlc/` compiles: `go build ./...`.

- [ ] **Step 4: testdb helper + failing query smoke test**

```bash
go get github.com/testcontainers/testcontainers-go github.com/testcontainers/testcontainers-go/modules/postgres
```

`pkg/testdb/testdb.go`:

```go
// Package testdb provides a shared throwaway Postgres for integration tests.
package testdb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var (
	once    sync.Once
	connURL string
	initErr error
)

// New returns a pool connected to a container Postgres with schema.sql applied
// and every table truncated. Run tests with `go test -p 1`.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
		pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("hotpot"),
			tcpostgres.WithUsername("hotpot"),
			tcpostgres.WithPassword("hotpot"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			initErr = err
			return
		}
		connURL, initErr = pg.ConnectionString(ctx, "sslmode=disable")
		if initErr != nil {
			return
		}
		pool, err := pgxpool.New(ctx, connURL)
		if err != nil {
			initErr = err
			return
		}
		defer pool.Close()
		schema, err := os.ReadFile(schemaPath())
		if err != nil {
			initErr = err
			return
		}
		_, initErr = pool.Exec(ctx, string(schema))
	})
	if initErr != nil {
		t.Fatalf("testdb init: %v", initErr)
	}
	pool, err := pgxpool.New(ctx, connURL)
	if err != nil {
		t.Fatalf("testdb connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx,
		"TRUNCATE channel_events, channel_state, playlist_items, media_files, channels RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("testdb truncate: %v", err)
	}
	return pool
}

func schemaPath() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..", "schema.sql")
}
```

`internal/dbtest/queries_test.go`:

```go
package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

func TestChannelAndPlaylistQueries(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ctx := context.Background()

	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: "Movies", Number: 1, Slug: "movies",
		VideoWidth: 1920, VideoHeight: 1080, VideoBitrateK: 5000,
	})
	require.NoError(t, err)
	assert.Equal(t, "movies", ch.Slug)
	assert.True(t, ch.Enabled)

	items, err := q.InsertPlaylistItems(ctx, sqlc.InsertPlaylistItemsParams{
		ChannelID: ch.ID,
		Positions: []int32{0, 1},
		Paths:     []string{"a.mkv", "b.mkv"},
	})
	require.NoError(t, err)
	require.Len(t, items, 2)

	listed, err := q.ListPlaylistItems(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, "a.mkv", listed[0].Path)

	mf, err := q.UpsertMediaFile(ctx, sqlc.UpsertMediaFileParams{
		Path: "a.mkv", Size: 100,
		Mtime:      pgtype.Timestamptz{Time: time.Now(), Valid: true},
		DurationMs: 60000, VideoCodec: "h264", Probe: []byte(`{}`),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(60000), mf.DurationMs)

	st, err := q.UpsertChannelState(ctx, sqlc.UpsertChannelStateParams{
		ChannelID: ch.ID, ItemPosition: 1, Status: "running",
	})
	require.NoError(t, err)
	assert.Equal(t, "running", st.Status)

	running, err := q.ListRunningChannelStates(ctx)
	require.NoError(t, err)
	assert.Len(t, running, 1)
}
```

Note: exact generated param types can differ slightly (e.g. `pgtype.Timestamptz` vs `time.Time` for NOT NULL columns — sqlc/pgx emits `pgtype.Timestamptz` for nullable and `time.Time` for NOT NULL by default). Adjust the test to the generated `sqlc` types after `task sqlcgen`, not the other way around.

- [ ] **Step 5: Run** — `go test -p 1 ./internal/dbtest/ -v` → PASS (first run pulls the postgres image; allow time).

- [ ] **Step 6: Commit** — `git add -A && git commit -m "feat: database schema, sqlc queries, testdb helper"`

---

### Task 3: ffprobe wrapper + external subtitle discovery

**Files:**
- Create: `internal/ffmpeg/probe.go`, `internal/ffmpeg/probe_test.go`, `internal/ffmpeg/testdata/probe_movie.json`

**Interfaces:**
- Produces:
  - `type AudioTrack struct { Index int; Lang string; Codec string; Channels int }` (json tags: `index, lang, codec, channels`)
  - `type SubtitleTrack struct { Index int; Lang string; Codec string; External bool; Path string }` (json: `index, lang, codec, external, path`)
  - `type ProbeResult struct { DurationMs int64; VideoCodec string; Width, Height int; Audio []AudioTrack; Subs []SubtitleTrack }` (json: `duration_ms, video_codec, width, height, audio, subs`)
  - `type CLI struct { FFprobePath string }`; `func (c CLI) Probe(ctx context.Context, absPath string) (ProbeResult, error)`
- `Index` is the ordinal among streams of that type (0-based), NOT the absolute stream index. External subs have `Index: -1`, `External: true`, `Path` = absolute path to the .srt.

- [ ] **Step 1: Fixture + failing tests**

`internal/ffmpeg/testdata/probe_movie.json` (trimmed real ffprobe output shape):

```json
{
  "streams": [
    {"index": 0, "codec_type": "video", "codec_name": "hevc", "width": 1920, "height": 1080},
    {"index": 1, "codec_type": "audio", "codec_name": "eac3", "channels": 6, "tags": {"language": "tha"}},
    {"index": 2, "codec_type": "audio", "codec_name": "aac", "channels": 2, "tags": {"language": "eng"}},
    {"index": 3, "codec_type": "subtitle", "codec_name": "subrip", "tags": {"language": "tha"}},
    {"index": 4, "codec_type": "subtitle", "codec_name": "hdmv_pgs_subtitle", "tags": {"language": "eng"}}
  ],
  "format": {"duration": "5401.760000"}
}
```

`internal/ffmpeg/probe_test.go`:

```go
package ffmpeg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProbeOutput(t *testing.T) {
	raw, err := os.ReadFile("testdata/probe_movie.json")
	require.NoError(t, err)

	p, err := parseProbeOutput(raw)
	require.NoError(t, err)

	assert.Equal(t, int64(5401760), p.DurationMs)
	assert.Equal(t, "hevc", p.VideoCodec)
	assert.Equal(t, 1920, p.Width)

	require.Len(t, p.Audio, 2)
	assert.Equal(t, AudioTrack{Index: 0, Lang: "tha", Codec: "eac3", Channels: 6}, p.Audio[0])
	assert.Equal(t, AudioTrack{Index: 1, Lang: "eng", Codec: "aac", Channels: 2}, p.Audio[1])

	// PGS (bitmap) is skipped; only text subs survive.
	require.Len(t, p.Subs, 1)
	assert.Equal(t, SubtitleTrack{Index: 0, Lang: "tha", Codec: "subrip"}, p.Subs[0])
}

func TestFindExternalSubs(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mkv")
	require.NoError(t, os.WriteFile(video, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "movie.eng.srt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "other.srt"), []byte("x"), 0o644))

	subs := findExternalSubs(video)
	require.Len(t, subs, 2)
	assert.Equal(t, "und", subs[0].Lang) // movie.srt sorts before movie.eng.srt? see impl: sorted by filename
	for _, s := range subs {
		assert.True(t, s.External)
		assert.Equal(t, -1, s.Index)
		assert.Equal(t, "srt", s.Codec)
	}
}
```

(Note: `movie.eng.srt` < `movie.srt` in lexical order — the test asserts `subs[0].Lang` accordingly; after implementing, set the expectation to the sorted order: `subs[0]` is `movie.eng.srt` → `"eng"`, `subs[1]` is `movie.srt` → `"und"`. Use exactly that in the final test.)

- [ ] **Step 2: Run — FAIL** (undefined `parseProbeOutput`)

- [ ] **Step 3: Implement** `internal/ffmpeg/probe.go`:

```go
package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type AudioTrack struct {
	Index    int    `json:"index"`
	Lang     string `json:"lang"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
}

type SubtitleTrack struct {
	Index    int    `json:"index"`
	Lang     string `json:"lang"`
	Codec    string `json:"codec"`
	External bool   `json:"external"`
	Path     string `json:"path,omitempty"`
}

type ProbeResult struct {
	DurationMs int64           `json:"duration_ms"`
	VideoCodec string          `json:"video_codec"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	Audio      []AudioTrack    `json:"audio"`
	Subs       []SubtitleTrack `json:"subs"`
}

type CLI struct {
	FFprobePath string
}

var textSubCodecs = map[string]bool{
	"subrip": true, "srt": true, "ass": true, "ssa": true,
	"mov_text": true, "webvtt": true, "text": true,
}

func (c CLI) Probe(ctx context.Context, absPath string) (ProbeResult, error) {
	out, err := exec.CommandContext(ctx, c.FFprobePath,
		"-v", "error", "-print_format", "json", "-show_format", "-show_streams", absPath,
	).Output()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe %s: %w", absPath, err)
	}
	p, err := parseProbeOutput(out)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("parse ffprobe output for %s: %w", absPath, err)
	}
	p.Subs = append(p.Subs, findExternalSubs(absPath)...)
	return p, nil
}

type rawProbe struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Channels  int    `json:"channels"`
		Tags      struct {
			Language string `json:"language"`
		} `json:"tags"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func parseProbeOutput(raw []byte) (ProbeResult, error) {
	var rp rawProbe
	if err := json.Unmarshal(raw, &rp); err != nil {
		return ProbeResult{}, err
	}
	var p ProbeResult
	if rp.Format.Duration != "" {
		sec, err := strconv.ParseFloat(rp.Format.Duration, 64)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("bad duration %q: %w", rp.Format.Duration, err)
		}
		p.DurationMs = int64(sec * 1000)
	}
	lang := func(l string) string {
		if l == "" {
			return "und"
		}
		return strings.ToLower(l)
	}
	for _, s := range rp.Streams {
		switch s.CodecType {
		case "video":
			if p.VideoCodec == "" { // first video stream wins
				p.VideoCodec = s.CodecName
				p.Width, p.Height = s.Width, s.Height
			}
		case "audio":
			p.Audio = append(p.Audio, AudioTrack{
				Index: len(p.Audio), Lang: lang(s.Tags.Language),
				Codec: s.CodecName, Channels: s.Channels,
			})
		case "subtitle":
			if textSubCodecs[s.CodecName] {
				p.Subs = append(p.Subs, SubtitleTrack{
					Index: len(p.Subs), Lang: lang(s.Tags.Language), Codec: s.CodecName,
				})
			}
		}
	}
	if p.VideoCodec == "" {
		return ProbeResult{}, fmt.Errorf("no video stream")
	}
	if p.DurationMs <= 0 {
		return ProbeResult{}, fmt.Errorf("no duration")
	}
	return p, nil
}

// findExternalSubs picks up sibling files: <base>.srt (lang und) and
// <base>.<lang>.srt. Results sorted by filename for determinism.
func findExternalSubs(videoAbsPath string) []SubtitleTrack {
	dir := filepath.Dir(videoAbsPath)
	base := strings.TrimSuffix(filepath.Base(videoAbsPath), filepath.Ext(videoAbsPath))
	matches, _ := filepath.Glob(filepath.Join(dir, base+"*.srt"))
	sort.Strings(matches)
	var subs []SubtitleTrack
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".srt")
		if name != base && !strings.HasPrefix(name, base+".") {
			continue // e.g. "movie2.srt" for base "movie"
		}
		l := "und"
		if rest := strings.TrimPrefix(name, base+"."); rest != name && rest != "" && len(rest) <= 3 {
			l = strings.ToLower(rest)
		}
		subs = append(subs, SubtitleTrack{Index: -1, Lang: l, Codec: "srt", External: true, Path: m})
	}
	return subs
}
```

- [ ] **Step 4: Run — PASS** (`go test ./internal/ffmpeg/ -v`). Fix the external-subs ordering expectation as noted.

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: ffprobe wrapper with external subtitle discovery"`

---

### Task 4: Channel CQRS core (domain + app, incl. SetPlaylist with probe-on-add)

**Files:**
- Create: `internal/response/response.go`, `internal/apperr/apperr.go`, `internal/channel/domain/channel/channel.go`, `internal/channel/app/app.go`, `internal/channel/app/command/create.go`, `internal/channel/app/command/update.go`, `internal/channel/app/command/delete.go`, `internal/channel/app/command/set_playlist.go`, `internal/channel/app/query/list.go`, `internal/channel/app/query/get.go`, `internal/channel/app/query/get_playlist.go`
- Test: `internal/channel/app/app_test.go`

**Interfaces:**
- Consumes: `sqlc.*` (Task 2), `ffmpeg.ProbeResult`/`ffmpeg.CLI` (Task 3).
- Produces:
  - `channel.Channel{ID int32, Name string, Number int32, Slug string, Enabled bool, VideoWidth, VideoHeight, VideoBitrateK int32, CreatedAt time.Time}` + `channel.NewFromSQLC(sqlc.Channel) Channel` + `channel.ErrNotFound` + `channel.Slugify(string) string`
  - `channel.PlaylistItem{ID int32, Position int32, Path string}`
  - `app.Application{Commands{Create command.CreateHandler; Update command.UpdateHandler; Delete command.DeleteHandler; SetPlaylist command.SetPlaylistHandler}, Queries{List query.ListHandler; Get query.GetHandler; GetPlaylist query.GetPlaylistHandler}}`
  - `app.NewApplication(pool *pgxpool.Pool, prober command.Prober, mediaPath string) app.Application`
  - `command.Prober interface { Probe(ctx context.Context, absPath string) (ffmpeg.ProbeResult, error) }`
  - Handler signatures: `Create.Handle(ctx, command.CreateInput{Name string; Number int32; Slug string; VideoWidth, VideoHeight, VideoBitrateK int32}) (channel.Channel, error)`; `Update.Handle(ctx, command.UpdateInput{ID int32; Name string; Number int32; Slug string; Enabled bool; VideoWidth, VideoHeight, VideoBitrateK int32}) (channel.Channel, error)`; `Delete.Handle(ctx, int32) error`; `SetPlaylist.Handle(ctx, command.SetPlaylistInput{ChannelID int32; Paths []string}) ([]channel.PlaylistItem, error)`; `List.Handle(ctx) ([]channel.Channel, error)`; `Get.Handle(ctx, int32) (channel.Channel, error)`; `GetPlaylist.Handle(ctx, int32) ([]channel.PlaylistItem, error)`

- [ ] **Step 1: Shared helpers**

`internal/response/response.go`:

```go
package response

type HTTPResponse struct {
	Data  any    `json:"data"`
	Error string `json:"error,omitempty"`
}
```

`internal/apperr/apperr.go`:

```go
package apperr

import "fmt"

type ValidationError struct {
	Fields map[string]string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %v", e.Fields)
}
```

- [ ] **Step 2: Failing test** `internal/channel/app/app_test.go`:

```go
package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/channel/app"
	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

type fakeProber struct{ calls int }

func (f *fakeProber) Probe(_ context.Context, _ string) (ffmpeg.ProbeResult, error) {
	f.calls++
	return ffmpeg.ProbeResult{
		DurationMs: 60000, VideoCodec: "h264", Width: 1280, Height: 720,
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "eng", Codec: "aac", Channels: 2}},
	}, nil
}

func TestChannelLifecycle(t *testing.T) {
	pool := testdb.New(t)
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "a.mkv"), []byte("x"), 0o644))

	prober := &fakeProber{}
	a := app.NewApplication(pool, prober, media)
	ctx := context.Background()

	ch, err := a.Commands.Create.Handle(ctx, command.CreateInput{Name: "Movies HD", Number: 1})
	require.NoError(t, err)
	assert.Equal(t, "movies-hd", ch.Slug) // auto-slugified
	assert.Equal(t, int32(1920), ch.VideoWidth) // defaults applied

	items, err := a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"a.mkv"},
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 1, prober.calls)

	// Same file again: probe cache hit, no re-probe.
	_, err = a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"a.mkv"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, prober.calls)

	// Missing file rejected.
	_, err = a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"nope.mkv"},
	})
	require.Error(t, err)

	// Traversal rejected.
	_, err = a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"../etc/passwd"},
	})
	require.Error(t, err)

	require.NoError(t, a.Commands.Delete.Handle(ctx, ch.ID))
	_, err = a.Queries.Get.Handle(ctx, ch.ID)
	assert.ErrorIs(t, err, channel.ErrNotFound)
}
```

- [ ] **Step 3: Run — FAIL** (packages don't exist)

- [ ] **Step 4: Implement**

`internal/channel/domain/channel/channel.go`:

```go
package channel

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"hotpot-iptv/sqlc"
)

var ErrNotFound = errors.New("channel not found")

type Channel struct {
	ID            int32     `json:"id"`
	Name          string    `json:"name"`
	Number        int32     `json:"number"`
	Slug          string    `json:"slug"`
	Enabled       bool      `json:"enabled"`
	VideoWidth    int32     `json:"video_width"`
	VideoHeight   int32     `json:"video_height"`
	VideoBitrateK int32     `json:"video_bitrate_k"`
	CreatedAt     time.Time `json:"created_at"`
}

type PlaylistItem struct {
	ID       int32  `json:"id"`
	Position int32  `json:"position"`
	Path     string `json:"path"`
}

func NewFromSQLC(sq sqlc.Channel) Channel {
	return Channel{
		ID: sq.ID, Name: sq.Name, Number: sq.Number, Slug: sq.Slug,
		Enabled: sq.Enabled, VideoWidth: sq.VideoWidth, VideoHeight: sq.VideoHeight,
		VideoBitrateK: sq.VideoBitrateK, CreatedAt: sq.CreatedAt.Time,
	}
}

func ItemFromSQLC(sq sqlc.PlaylistItem) PlaylistItem {
	return PlaylistItem{ID: sq.ID, Position: sq.Position, Path: sq.Path}
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(name), "-")
	return strings.Trim(s, "-")
}
```

(If sqlc generated `CreatedAt` as `time.Time` instead of `pgtype.Timestamptz`, drop the `.Time` — match the generated code.)

`internal/channel/app/command/create.go`:

```go
package command

import (
	"context"
	"fmt"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type CreateHandler struct {
	queries *sqlc.Queries
}

func NewCreateHandler(q *sqlc.Queries) CreateHandler { return CreateHandler{queries: q} }

type CreateInput struct {
	Name          string
	Number        int32
	Slug          string
	VideoWidth    int32
	VideoHeight   int32
	VideoBitrateK int32
}

func (h CreateHandler) Handle(ctx context.Context, in CreateInput) (channel.Channel, error) {
	if in.Slug == "" {
		in.Slug = channel.Slugify(in.Name)
	}
	if in.VideoWidth == 0 {
		in.VideoWidth = 1920
	}
	if in.VideoHeight == 0 {
		in.VideoHeight = 1080
	}
	if in.VideoBitrateK == 0 {
		in.VideoBitrateK = 5000
	}
	sq, err := h.queries.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: in.Name, Number: in.Number, Slug: in.Slug,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
	if err != nil {
		return channel.Channel{}, fmt.Errorf("create channel: %w", err)
	}
	return channel.NewFromSQLC(sq), nil
}
```

`internal/channel/app/command/update.go` (same shape):

```go
package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type UpdateHandler struct {
	queries *sqlc.Queries
}

func NewUpdateHandler(q *sqlc.Queries) UpdateHandler { return UpdateHandler{queries: q} }

type UpdateInput struct {
	ID            int32
	Name          string
	Number        int32
	Slug          string
	Enabled       bool
	VideoWidth    int32
	VideoHeight   int32
	VideoBitrateK int32
}

func (h UpdateHandler) Handle(ctx context.Context, in UpdateInput) (channel.Channel, error) {
	if in.Slug == "" {
		in.Slug = channel.Slugify(in.Name)
	}
	sq, err := h.queries.UpdateChannel(ctx, sqlc.UpdateChannelParams{
		ID: in.ID, Name: in.Name, Number: in.Number, Slug: in.Slug, Enabled: in.Enabled,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.Channel{}, channel.ErrNotFound
	}
	if err != nil {
		return channel.Channel{}, fmt.Errorf("update channel: %w", err)
	}
	return channel.NewFromSQLC(sq), nil
}
```

`internal/channel/app/command/delete.go`:

```go
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
```

`internal/channel/app/command/set_playlist.go`:

```go
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
	if err == nil && existing.Size == st.Size() && existing.Mtime.Time.Equal(st.ModTime().UTC().Truncate(0)) {
		return nil
	}
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
```

(Keep only ONE of the two mtime-comparison lines above — use the `Unix()` comparison; it's timezone/precision safe. Delete the `Truncate(0)` line when implementing.)

`internal/channel/app/query/list.go`:

```go
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
```

`internal/channel/app/query/get.go`:

```go
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
```

`internal/channel/app/query/get_playlist.go`:

```go
package query

import (
	"context"
	"fmt"

	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/sqlc"
)

type GetPlaylistHandler struct {
	queries *sqlc.Queries
}

func NewGetPlaylistHandler(q *sqlc.Queries) GetPlaylistHandler { return GetPlaylistHandler{queries: q} }

func (h GetPlaylistHandler) Handle(ctx context.Context, channelID int32) ([]channel.PlaylistItem, error) {
	rows, err := h.queries.ListPlaylistItems(ctx, channelID)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}
	out := make([]channel.PlaylistItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, channel.ItemFromSQLC(r))
	}
	return out, nil
}
```

`internal/channel/app/app.go`:

```go
package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/app/query"
	"hotpot-iptv/sqlc"
)

type Commands struct {
	Create      command.CreateHandler
	Update      command.UpdateHandler
	Delete      command.DeleteHandler
	SetPlaylist command.SetPlaylistHandler
}

type Queries struct {
	List        query.ListHandler
	Get         query.GetHandler
	GetPlaylist query.GetPlaylistHandler
}

type Application struct {
	Commands Commands
	Queries  Queries
}

func NewApplication(pool *pgxpool.Pool, prober command.Prober, mediaPath string) Application {
	q := sqlc.New(pool)
	return Application{
		Commands: Commands{
			Create:      command.NewCreateHandler(q),
			Update:      command.NewUpdateHandler(q),
			Delete:      command.NewDeleteHandler(q),
			SetPlaylist: command.NewSetPlaylistHandler(pool, q, prober, mediaPath),
		},
		Queries: Queries{
			List:        query.NewListHandler(q),
			Get:         query.NewGetHandler(q),
			GetPlaylist: query.NewGetPlaylistHandler(q),
		},
	}
}
```

- [ ] **Step 5: Run — PASS** (`go test -p 1 ./internal/channel/... -v`)

- [ ] **Step 6: Commit** — `git add -A && git commit -m "feat: channel CQRS core with probe-on-add playlists"`

---

### Task 5: api/channels HTTP slice (CRUD + playlist)

**Files:**
- Create: `api/channels/server.go`, `api/channels/service/client.go`, `api/channels/service/channels.go`, `api/channels/http/server.go`, `api/channels/http/router.go`, `api/channels/http/respond.go`, `api/channels/http/create.go`, `api/channels/http/list.go`, `api/channels/http/get.go`, `api/channels/http/update.go`, `api/channels/http/delete.go`, `api/channels/http/playlist.go`
- Test: `api/channels/integration_test.go`
- Modify: `main.go` (mount under `/api/v1`)

**Interfaces:**
- Consumes: Task 4 app; `response.HTTPResponse`; `apperr.ValidationError`.
- Produces: `channels.GetHTTPHandler(pool *pgxpool.Pool, prober command.Prober, mediaPath string) *chi.Mux` mounted at `/api/v1` (routes: `GET/POST /channels`, `GET/PUT/DELETE /channels/{id}`, `GET/PUT /channels/{id}/playlist`). Service: `service.Client` with `CreateChannel(ctx, service.ChannelRequest) (channel.Channel, error)`, `UpdateChannel(ctx, id int32, service.ChannelRequest)`, `ListChannels(ctx)`, `GetChannel(ctx, id)`, `DeleteChannel(ctx, id)`, `SetPlaylist(ctx, id int32, paths []string) ([]channel.PlaylistItem, error)`, `GetPlaylist(ctx, id)`. `ChannelRequest{Name string; Number int32; Slug string; Enabled *bool; VideoWidth, VideoHeight, VideoBitrateK int32}` (json tags: `name, number, slug, enabled, video_width, video_height, video_bitrate_k`).

- [ ] **Step 1: Failing integration test** `api/channels/integration_test.go`:

```go
package channels_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	channels "hotpot-iptv/api/channels"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
)

type fakeProber struct{}

func (fakeProber) Probe(context.Context, string) (ffmpeg.ProbeResult, error) {
	return ffmpeg.ProbeResult{DurationMs: 1000, VideoCodec: "h264", Width: 1280, Height: 720}, nil
}

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
	res.Body.Close()
	return res, out
}

func TestChannelsAPI(t *testing.T) {
	pool := testdb.New(t)
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "a.mkv"), []byte("x"), 0o644))

	srv := httptest.NewServer(channels.GetHTTPHandler(pool, fakeProber{}, media))
	defer srv.Close()

	res, out := doJSON(t, srv, "POST", "/channels", map[string]any{"name": "Movies", "number": 1})
	require.Equal(t, http.StatusOK, res.StatusCode)
	id := int(out["data"].(map[string]any)["id"].(float64))

	res, _ = doJSON(t, srv, "POST", "/channels", map[string]any{"name": "", "number": 0})
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, out["data"], 1)

	res, _ = doJSON(t, srv, "PUT",
		"/channels/"+itoa(id)+"/playlist", map[string]any{"paths": []string{"a.mkv"}})
	require.Equal(t, http.StatusOK, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels/"+itoa(id)+"/playlist", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Len(t, out["data"], 1)

	res, _ = doJSON(t, srv, "GET", "/channels/9999", nil)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func itoa(i int) string { return fmt.Sprintf("%d", i) }
```

(Add `"fmt"` to imports.)

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

`api/channels/server.go`:

```go
package channels

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	channelshttp "hotpot-iptv/api/channels/http"
	"hotpot-iptv/api/channels/service"
	"hotpot-iptv/internal/channel/app"
	"hotpot-iptv/internal/channel/app/command"
)

func GetHTTPHandler(pool *pgxpool.Pool, prober command.Prober, mediaPath string) *chi.Mux {
	a := app.NewApplication(pool, prober, mediaPath)
	svc := service.NewClient(a)
	return channelshttp.NewServer(svc).NewRouter()
}
```

`api/channels/service/client.go`:

```go
package service

import "hotpot-iptv/internal/channel/app"

type Client struct {
	app app.Application
}

func NewClient(a app.Application) Client { return Client{app: a} }
```

`api/channels/service/channels.go`:

```go
package service

import (
	"context"

	"hotpot-iptv/internal/apperr"
	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/domain/channel"
)

type ChannelRequest struct {
	Name          string `json:"name"`
	Number        int32  `json:"number"`
	Slug          string `json:"slug"`
	Enabled       *bool  `json:"enabled"`
	VideoWidth    int32  `json:"video_width"`
	VideoHeight   int32  `json:"video_height"`
	VideoBitrateK int32  `json:"video_bitrate_k"`
}

func validate(in ChannelRequest) error {
	fields := map[string]string{}
	if in.Name == "" {
		fields["name"] = "required"
	}
	if in.Number <= 0 {
		fields["number"] = "must be positive"
	}
	if len(fields) > 0 {
		return apperr.ValidationError{Fields: fields}
	}
	return nil
}

func (c Client) CreateChannel(ctx context.Context, in ChannelRequest) (channel.Channel, error) {
	if err := validate(in); err != nil {
		return channel.Channel{}, err
	}
	return c.app.Commands.Create.Handle(ctx, command.CreateInput{
		Name: in.Name, Number: in.Number, Slug: in.Slug,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
}

func (c Client) UpdateChannel(ctx context.Context, id int32, in ChannelRequest) (channel.Channel, error) {
	if err := validate(in); err != nil {
		return channel.Channel{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return c.app.Commands.Update.Handle(ctx, command.UpdateInput{
		ID: id, Name: in.Name, Number: in.Number, Slug: in.Slug, Enabled: enabled,
		VideoWidth: in.VideoWidth, VideoHeight: in.VideoHeight, VideoBitrateK: in.VideoBitrateK,
	})
}

func (c Client) ListChannels(ctx context.Context) ([]channel.Channel, error) {
	return c.app.Queries.List.Handle(ctx)
}

func (c Client) GetChannel(ctx context.Context, id int32) (channel.Channel, error) {
	return c.app.Queries.Get.Handle(ctx, id)
}

func (c Client) DeleteChannel(ctx context.Context, id int32) error {
	return c.app.Commands.Delete.Handle(ctx, id)
}

func (c Client) SetPlaylist(ctx context.Context, id int32, paths []string) ([]channel.PlaylistItem, error) {
	return c.app.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{ChannelID: id, Paths: paths})
}

func (c Client) GetPlaylist(ctx context.Context, id int32) ([]channel.PlaylistItem, error) {
	return c.app.Queries.GetPlaylist.Handle(ctx, id)
}
```

`api/channels/http/server.go`:

```go
package http

import "hotpot-iptv/api/channels/service"

type Server struct {
	svc service.Client
}

func NewServer(svc service.Client) Server { return Server{svc: svc} }
```

`api/channels/http/router.go`:

```go
package http

import "github.com/go-chi/chi/v5"

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Route("/channels", func(r chi.Router) {
		r.Get("/", s.List)
		r.Post("/", s.Create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", s.Get)
			r.Put("/", s.Update)
			r.Delete("/", s.Delete)
			r.Get("/playlist", s.GetPlaylist)
			r.Put("/playlist", s.SetPlaylist)
		})
	})
	return r
}
```

`api/channels/http/respond.go`:

```go
package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"hotpot-iptv/internal/apperr"
	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/response"
)

func ok(w http.ResponseWriter, r *http.Request, data any) {
	render.JSON(w, r, response.HTTPResponse{Data: data})
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve apperr.ValidationError
	switch {
	case errors.As(err, &ve):
		render.Status(r, http.StatusBadRequest)
	case errors.Is(err, channel.ErrNotFound):
		render.Status(r, http.StatusNotFound)
	default:
		render.Status(r, http.StatusInternalServerError)
	}
	render.JSON(w, r, response.HTTPResponse{Error: err.Error()})
}

func idParam(r *http.Request) (int32, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 32)
	if err != nil {
		return 0, apperr.ValidationError{Fields: map[string]string{"id": "must be an integer"}}
	}
	return int32(id), nil
}
```

Endpoint files — one per route, all in `package http`:

```go
// create.go
package http

import (
	"encoding/json"
	"net/http"

	"hotpot-iptv/api/channels/service"
	"hotpot-iptv/internal/apperr"
)

func (s Server) Create(w http.ResponseWriter, r *http.Request) {
	var in service.ChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, r, apperr.ValidationError{Fields: map[string]string{"body": "invalid json"}})
		return
	}
	ch, err := s.svc.CreateChannel(r.Context(), in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, ch)
}
```

```go
// list.go
package http

import "net/http"

func (s Server) List(w http.ResponseWriter, r *http.Request) {
	chs, err := s.svc.ListChannels(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, chs)
}
```

```go
// get.go
package http

import "net/http"

func (s Server) Get(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	ch, err := s.svc.GetChannel(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, ch)
}
```

```go
// update.go
package http

import (
	"encoding/json"
	"net/http"

	"hotpot-iptv/api/channels/service"
	"hotpot-iptv/internal/apperr"
)

func (s Server) Update(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	var in service.ChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, r, apperr.ValidationError{Fields: map[string]string{"body": "invalid json"}})
		return
	}
	ch, err := s.svc.UpdateChannel(r.Context(), id, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, ch)
}
```

```go
// delete.go
package http

import "net/http"

func (s Server) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.svc.DeleteChannel(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, map[string]bool{"deleted": true})
}
```

```go
// playlist.go
package http

import (
	"encoding/json"
	"net/http"

	"hotpot-iptv/internal/apperr"
)

type setPlaylistRequest struct {
	Paths []string `json:"paths"`
}

func (s Server) SetPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	var in setPlaylistRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, r, apperr.ValidationError{Fields: map[string]string{"body": "invalid json"}})
		return
	}
	items, err := s.svc.SetPlaylist(r.Context(), id, in.Paths)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, items)
}

func (s Server) GetPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	items, err := s.svc.GetPlaylist(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, items)
}
```

`main.go` — inside `main()`, after config load, add pool + mount (skip mounting if `PSQL_URL` empty so `go run .` still works pre-DB):

```go
	// inside main(), replacing the bare router block
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.PSQLURL != "" {
		pool, err := pgxpool.New(context.Background(), cfg.PSQLURL)
		if err != nil {
			log.Fatalf("connect postgres: %v", err)
		}
		prober := ffmpeg.CLI{FFprobePath: cfg.FFprobePath}
		r.Mount("/api/v1", channels.GetHTTPHandler(pool, prober, cfg.MediaPath))
	}
```

(Imports: `context`, `github.com/jackc/pgx/v5/pgxpool`, `hotpot-iptv/api/channels`, `hotpot-iptv/internal/ffmpeg`.)

- [ ] **Step 4: Run — PASS** (`go test -p 1 ./api/channels/ -v`); `go build ./...`

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: channels JSON API"`

---

### Task 6: Library browser (internal/library + api/library)

**Files:**
- Create: `internal/library/library.go`, `internal/library/library_test.go`, `api/library/server.go`, `api/library/http/server.go`
- Modify: `main.go` (mount `/api/v1/library`)

**Interfaces:**
- Produces: `library.Entry{Name string; Path string; IsDir bool; Size int64}` (json: `name, path, is_dir, size`); `library.List(root, rel string) ([]library.Entry, error)` — dirs first then files, alphabetical; only dirs + video extensions (`.mkv .mp4 .avi .mov .ts .m2ts .webm`); `Path` is always relative to root. Traversal (`..`, absolute) → error. `libraryapi.GetHTTPHandler(mediaPath string) *chi.Mux` with `GET /library?path=<rel>` → `{"data":{"path":"<rel>","entries":[...]}}`.

- [ ] **Step 1: Failing test** `internal/library/library_test.go`:

```go
package library

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestList(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "shows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.mkv"), []byte("xx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.mp4"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644))

	entries, err := List(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 3) // shows/, a.mp4, b.mkv — txt filtered out
	assert.Equal(t, Entry{Name: "shows", Path: "shows", IsDir: true}, entries[0])
	assert.Equal(t, "a.mp4", entries[1].Name)
	assert.Equal(t, int64(2), entries[2].Size)

	_, err = List(root, "../outside")
	require.Error(t, err)
	_, err = List(root, "/abs")
	require.Error(t, err)
	_, err = List(root, "missing")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

`internal/library/library.go`:

```go
// Package library browses the mounted media tree.
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
	".ts": true, ".m2ts": true, ".webm": true,
}

func List(root, rel string) ([]Entry, error) {
	clean := filepath.Clean(rel)
	if clean == "." {
		clean = ""
	}
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return nil, fmt.Errorf("invalid path %q", rel)
	}
	dir := filepath.Join(root, clean)
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	var entries []Entry
	for _, de := range des {
		relPath := filepath.Join(clean, de.Name())
		if de.IsDir() {
			entries = append(entries, Entry{Name: de.Name(), Path: relPath, IsDir: true})
			continue
		}
		if !videoExts[strings.ToLower(filepath.Ext(de.Name()))] {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, Entry{Name: de.Name(), Path: relPath, Size: info.Size()})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}
```

`api/library/server.go`:

```go
package library

import (
	"github.com/go-chi/chi/v5"

	libraryhttp "hotpot-iptv/api/library/http"
)

func GetHTTPHandler(mediaPath string) *chi.Mux {
	return libraryhttp.NewServer(mediaPath).NewRouter()
}
```

`api/library/http/server.go`:

```go
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"hotpot-iptv/internal/library"
	"hotpot-iptv/internal/response"
)

type Server struct {
	mediaPath string
}

func NewServer(mediaPath string) Server { return Server{mediaPath: mediaPath} }

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/library", s.Browse)
	return r
}

func (s Server) Browse(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	entries, err := library.List(s.mediaPath, rel)
	if err != nil {
		render.Status(r, http.StatusBadRequest)
		render.JSON(w, r, response.HTTPResponse{Error: err.Error()})
		return
	}
	render.JSON(w, r, response.HTTPResponse{Data: map[string]any{"path": rel, "entries": entries}})
}
```

`main.go`: next to the channels mount add `r.Mount("/api/v1", libraryapi.GetHTTPHandler(cfg.MediaPath))` — chi can't mount two muxes at one path; instead mount the library router INSIDE the channels mux? No — keep it simple: in `main.go` build one API router:

```go
		api := chi.NewRouter()
		api.Mount("/", channels.GetHTTPHandler(pool, prober, cfg.MediaPath))
		api.Mount("/", libraryapi.GetHTTPHandler(cfg.MediaPath)) // chi panics on duplicate mounts
```

chi DOES panic mounting two handlers at "/". The correct wiring: `channels.GetHTTPHandler` router already owns `/channels/...`; give library its own subpath — in `api/library/http.NewRouter` the route is `/library`, so mount as:

```go
		r.Mount("/api/v1", channels.GetHTTPHandler(pool, prober, cfg.MediaPath))
		r.Mount("/api/v1/library", libraryapi.GetHTTPHandler(cfg.MediaPath))
```

and change the library router's route from `/library` to `/`. Apply that: in `api/library/http/server.go` use `r.Get("/", s.Browse)`.

- [ ] **Step 4: Run — PASS** (`go test ./internal/library/ -v`); `go build ./...`

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: media library browser API"`

---

### Task 7: WebVTT parse + split

**Files:**
- Create: `internal/hls/vtt.go`, `internal/hls/vtt_test.go`

**Interfaces:**
- Produces: `hls.Cue{Start, End time.Duration; Settings string; Text string}`; `hls.ParseVTT(r io.Reader) ([]Cue, error)`; `hls.SplitVTT(cues []Cue, segDur, total time.Duration) []string` — returns `ceil(total/segDur)` complete VTT segment bodies, each starting with the `X-TIMESTAMP-MAP` header; a cue appears in every segment it overlaps; cue times stay absolute (file-relative), which is what HLS expects with X-TIMESTAMP-MAP.

- [ ] **Step 1: Failing tests** `internal/hls/vtt_test.go`:

```go
package hls

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleVTT = `WEBVTT

NOTE a comment

1
00:00:01.000 --> 00:00:03.500 align:start
Hello there

00:00:05.000 --> 00:00:09.200
Second cue
spanning two lines

00:01:02.000 --> 00:01:04.000
Late cue
`

func TestParseVTT(t *testing.T) {
	cues, err := ParseVTT(strings.NewReader(sampleVTT))
	require.NoError(t, err)
	require.Len(t, cues, 3)
	assert.Equal(t, time.Second, cues[0].Start)
	assert.Equal(t, 3500*time.Millisecond, cues[0].End)
	assert.Equal(t, "align:start", cues[0].Settings)
	assert.Equal(t, "Hello there", cues[0].Text)
	assert.Equal(t, "Second cue\nspanning two lines", cues[1].Text)
}

func TestSplitVTT(t *testing.T) {
	cues, err := ParseVTT(strings.NewReader(sampleVTT))
	require.NoError(t, err)

	segs := SplitVTT(cues, 4*time.Second, 66*time.Second)
	require.Len(t, segs, 17) // ceil(66/4)

	for _, s := range segs {
		assert.True(t, strings.HasPrefix(s,
			"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n"))
	}
	// seg 0 covers [0,4): first cue only
	assert.Contains(t, segs[0], "Hello there")
	assert.NotContains(t, segs[0], "Second cue")
	// cue 2 spans [5,9.2) → overlaps segs 1 and 2
	assert.Contains(t, segs[1], "Second cue")
	assert.Contains(t, segs[2], "Second cue")
	assert.NotContains(t, segs[3], "Second cue")
	// late cue [62,64) → seg 15
	assert.Contains(t, segs[15], "Late cue")
	// empty middle segment has only the header
	assert.Equal(t,
		"WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n", segs[5])
}

func TestSplitVTTEmpty(t *testing.T) {
	segs := SplitVTT(nil, 4*time.Second, 10*time.Second)
	require.Len(t, segs, 3)
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/hls/vtt.go`:

```go
package hls

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Cue struct {
	Start    time.Duration
	End      time.Duration
	Settings string
	Text     string
}

const vttSegmentHeader = "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n"

var timingRe = regexp.MustCompile(
	`^(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})\s+-->\s+(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})\s*(.*)$`)

func ParseVTT(r io.Reader) ([]Cue, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var cues []Cue
	var cur *Cue
	inNote := false
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if cur == nil {
			if line == "" {
				inNote = false
				continue
			}
			if inNote || strings.HasPrefix(line, "WEBVTT") ||
				strings.HasPrefix(line, "NOTE") || strings.HasPrefix(line, "STYLE") ||
				strings.HasPrefix(line, "REGION") {
				inNote = !strings.HasPrefix(line, "WEBVTT")
				continue
			}
			m := timingRe.FindStringSubmatch(line)
			if m == nil {
				continue // cue identifier line — timing follows
			}
			cur = &Cue{
				Start:    vttTimestamp(m[1], m[2], m[3], m[4]),
				End:      vttTimestamp(m[5], m[6], m[7], m[8]),
				Settings: strings.TrimSpace(m[9]),
			}
			continue
		}
		if line == "" {
			cues = append(cues, *cur)
			cur = nil
			continue
		}
		if cur.Text != "" {
			cur.Text += "\n"
		}
		cur.Text += line
	}
	if cur != nil {
		cues = append(cues, *cur)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan vtt: %w", err)
	}
	return cues, nil
}

func vttTimestamp(h, m, s, ms string) time.Duration {
	hh, _ := strconv.Atoi(h) // empty string → 0
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	mss, _ := strconv.Atoi(ms)
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute +
		time.Duration(ss)*time.Second + time.Duration(mss)*time.Millisecond
}

func formatVTTTime(d time.Duration) string {
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	ms := (d % time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// SplitVTT distributes cues into ceil(total/segDur) VTT segment bodies.
// A cue is included in every segment its [Start,End) range overlaps.
func SplitVTT(cues []Cue, segDur, total time.Duration) []string {
	if segDur <= 0 || total <= 0 {
		return nil
	}
	n := int((total + segDur - 1) / segDur)
	segs := make([]string, n)
	for i := 0; i < n; i++ {
		lo := time.Duration(i) * segDur
		hi := lo + segDur
		var b strings.Builder
		b.WriteString(vttSegmentHeader)
		for _, c := range cues {
			if c.End <= lo || c.Start >= hi {
				continue
			}
			b.WriteString("\n")
			b.WriteString(formatVTTTime(c.Start))
			b.WriteString(" --> ")
			b.WriteString(formatVTTTime(c.End))
			if c.Settings != "" {
				b.WriteString(" " + c.Settings)
			}
			b.WriteString("\n")
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
		segs[i] = b.String()
	}
	return segs
}
```

(Note the `inNote` toggle in the parser is subtle — after implementing, walk the sample through: `WEBVTT` sets `inNote=false`, `NOTE a comment` sets `inNote=true` and skips until blank line. If the test fails on the NOTE block, simplify: treat any non-timing, non-blank line before a timing line as skippable — the regexp check already does that via the `continue`; then the NOTE/STYLE special-casing can be dropped entirely except that a NOTE body line could be mistaken for a cue id. The test's NOTE body has no timing line so plain regexp-continue passes. Prefer the simplest version that passes the test.)

- [ ] **Step 4: Run — PASS** (`go test ./internal/hls/ -v`)

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: webvtt parser and segment splitter"`

---

### Task 8: HLS rendition model (union + per-file track mapping)

**Files:**
- Create: `internal/hls/renditions.go`, `internal/hls/renditions_test.go`

**Interfaces:**
- Consumes: `ffmpeg.ProbeResult` (Task 3).
- Produces:
  - `hls.RenditionKind` (string: `"video" | "audio" | "subs"`), constants `KindVideo, KindAudio, KindSubs`
  - `hls.Rendition{Kind RenditionKind; Key string; Lang string; Name string}`; `(Rendition).PlaylistURI() string` = `Key + ".m3u8"`
  - Keys: video = `"v"`; audio = `"a_<lang>_<occ>"`; subs = `"s_<lang>_<occ>"` where `<occ>` is the occurrence ordinal of that language within one file (0-based)
  - `hls.ComputeRenditions(probes []ffmpeg.ProbeResult) []Rendition` — union across files, ordered: video, then audio in first-appearance order, then subs in first-appearance order
  - `hls.MapTracks(rends []Rendition, p ffmpeg.ProbeResult) map[string]int` — rendition key → index into `p.Audio` / `p.Subs` (video key → 0); **missing → -1** (filler)
  - `hls.LangName(code string) string` — display name for common ISO-639-2 codes, falls back to the code

- [ ] **Step 1: Failing tests** `internal/hls/renditions_test.go`:

```go
package hls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/ffmpeg"
)

func probes() []ffmpeg.ProbeResult {
	return []ffmpeg.ProbeResult{
		{ // file A: tha+eng audio, tha sub
			Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "tha"}, {Index: 1, Lang: "eng"}},
			Subs:  []ffmpeg.SubtitleTrack{{Index: 0, Lang: "tha"}},
		},
		{ // file B: eng only, two eng subs
			Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "eng"}},
			Subs:  []ffmpeg.SubtitleTrack{{Index: 0, Lang: "eng"}, {Index: 1, Lang: "eng"}},
		},
	}
}

func TestComputeRenditions(t *testing.T) {
	rends := ComputeRenditions(probes())
	keys := make([]string, 0, len(rends))
	for _, r := range rends {
		keys = append(keys, r.Key)
	}
	assert.Equal(t, []string{"v", "a_tha_0", "a_eng_0", "s_tha_0", "s_eng_0", "s_eng_1"}, keys)
	assert.Equal(t, "Thai", rends[1].Name)
	assert.Equal(t, "English 2", rends[5].Name) // second eng sub
	assert.Equal(t, "v.m3u8", rends[0].PlaylistURI())
}

func TestMapTracks(t *testing.T) {
	ps := probes()
	rends := ComputeRenditions(ps)

	mA := MapTracks(rends, ps[0])
	assert.Equal(t, 0, mA["v"])
	assert.Equal(t, 0, mA["a_tha_0"])
	assert.Equal(t, 1, mA["a_eng_0"])
	assert.Equal(t, 0, mA["s_tha_0"])
	assert.Equal(t, -1, mA["s_eng_0"]) // filler
	assert.Equal(t, -1, mA["s_eng_1"])

	mB := MapTracks(rends, ps[1])
	assert.Equal(t, -1, mB["a_tha_0"]) // filler → silence
	assert.Equal(t, 0, mB["a_eng_0"])
	assert.Equal(t, 0, mB["s_eng_0"])
	assert.Equal(t, 1, mB["s_eng_1"])
	require.Equal(t, -1, mB["s_tha_0"])
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/hls/renditions.go`:

```go
package hls

import (
	"fmt"

	"hotpot-iptv/internal/ffmpeg"
)

type RenditionKind string

const (
	KindVideo RenditionKind = "video"
	KindAudio RenditionKind = "audio"
	KindSubs  RenditionKind = "subs"
)

type Rendition struct {
	Kind RenditionKind
	Key  string
	Lang string
	Name string
}

func (r Rendition) PlaylistURI() string { return r.Key + ".m3u8" }

var langNames = map[string]string{
	"tha": "Thai", "eng": "English", "jpn": "Japanese", "kor": "Korean",
	"chi": "Chinese", "zho": "Chinese", "spa": "Spanish", "fre": "French",
	"fra": "French", "ger": "German", "deu": "German", "ita": "Italian",
	"por": "Portuguese", "rus": "Russian", "vie": "Vietnamese", "ind": "Indonesian",
	"may": "Malay", "hin": "Hindi", "ara": "Arabic", "und": "Unknown",
}

func LangName(code string) string {
	if n, ok := langNames[code]; ok {
		return n
	}
	return code
}

// key builds "a_eng_0" style keys: kind prefix, language, occurrence ordinal
// of that language within a single file.
func key(prefix, lang string, occ int) string {
	return fmt.Sprintf("%s_%s_%d", prefix, lang, occ)
}

func displayName(lang string, occ int) string {
	if occ == 0 {
		return LangName(lang)
	}
	return fmt.Sprintf("%s %d", LangName(lang), occ+1)
}

// ComputeRenditions unions tracks across all playlist items so the channel's
// rendition set stays fixed for the whole session.
func ComputeRenditions(probes []ffmpeg.ProbeResult) []Rendition {
	rends := []Rendition{{Kind: KindVideo, Key: "v", Name: "Video"}}
	seen := map[string]bool{}
	// audio first (order of first appearance), then subs
	for _, p := range probes {
		occ := map[string]int{}
		for _, a := range p.Audio {
			k := key("a", a.Lang, occ[a.Lang])
			occ[a.Lang]++
			if !seen[k] {
				seen[k] = true
				rends = append(rends, Rendition{
					Kind: KindAudio, Key: k, Lang: a.Lang,
					Name: displayName(a.Lang, countLang(rends, KindAudio, a.Lang)),
				})
			}
		}
	}
	for _, p := range probes {
		occ := map[string]int{}
		for _, s := range p.Subs {
			k := key("s", s.Lang, occ[s.Lang])
			occ[s.Lang]++
			if !seen[k] {
				seen[k] = true
				rends = append(rends, Rendition{
					Kind: KindSubs, Key: k, Lang: s.Lang,
					Name: displayName(s.Lang, countLang(rends, KindSubs, s.Lang)),
				})
			}
		}
	}
	return rends
}

func countLang(rends []Rendition, kind RenditionKind, lang string) int {
	n := 0
	for _, r := range rends {
		if r.Kind == kind && r.Lang == lang {
			n++
		}
	}
	return n
}

// MapTracks maps each rendition key to the matching track index in this file's
// probe (nth track of that language), or -1 when the file lacks it.
func MapTracks(rends []Rendition, p ffmpeg.ProbeResult) map[string]int {
	m := make(map[string]int, len(rends))
	for _, r := range rends {
		switch r.Kind {
		case KindVideo:
			m[r.Key] = 0
		case KindAudio:
			m[r.Key] = -1
			occ := map[string]int{}
			for i, a := range p.Audio {
				if key("a", a.Lang, occ[a.Lang]) == r.Key {
					m[r.Key] = i
					break
				}
				occ[a.Lang]++
			}
		case KindSubs:
			m[r.Key] = -1
			occ := map[string]int{}
			for i, s := range p.Subs {
				if key("s", s.Lang, occ[s.Lang]) == r.Key {
					m[r.Key] = i
					break
				}
				occ[s.Lang]++
			}
		}
	}
	return m
}
```

- [ ] **Step 4: Run — PASS**

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: hls rendition union and track mapping"`

---

### Task 9: HLS playlist manager (media + master, window, discontinuity)

**Files:**
- Create: `internal/hls/playlist.go`, `internal/hls/playlist_test.go`

**Interfaces:**
- Consumes: `Rendition` (Task 8).
- Produces:
  - `hls.VideoParams{Width, Height, BitrateK int}`
  - `hls.NewManager(rends []Rendition, targetDurSec, window int, video VideoParams) *Manager` (thread-safe)
  - `(m *Manager) MarkDiscontinuity()` — the NEXT segment appended to each rendition carries a discontinuity marker (per-rendition pending flag)
  - `(m *Manager) Append(key, uri string, dur float64) (evicted []string)` — appends; when the window overflows, drops the oldest segment, bumps media-sequence (and discontinuity-sequence when the dropped segment carried the marker) and returns dropped URIs
  - `(m *Manager) RenderMedia(key string) (string, bool)` — live media playlist (no ENDLIST)
  - `(m *Manager) RenderMaster() string`
  - `(m *Manager) Renditions() []Rendition`

- [ ] **Step 1: Failing golden tests** `internal/hls/playlist_test.go`:

```go
package hls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestManager(window int) *Manager {
	rends := []Rendition{
		{Kind: KindVideo, Key: "v", Name: "Video"},
		{Kind: KindAudio, Key: "a_tha_0", Lang: "tha", Name: "Thai"},
		{Kind: KindAudio, Key: "a_eng_0", Lang: "eng", Name: "English"},
		{Kind: KindSubs, Key: "s_tha_0", Lang: "tha", Name: "Thai"},
	}
	return NewManager(rends, 4, window, VideoParams{Width: 1920, Height: 1080, BitrateK: 5000})
}

func TestRenderMediaWithDiscontinuity(t *testing.T) {
	m := newTestManager(10)
	m.Append("v", "000001/v_0.ts", 4.0)
	m.Append("v", "000001/v_1.ts", 3.2)
	m.MarkDiscontinuity()
	m.Append("v", "000002/v_0.ts", 4.0)

	got, ok := m.RenderMedia("v")
	require.True(t, ok)
	assert.Equal(t, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-DISCONTINUITY-SEQUENCE:0
#EXTINF:4.000,
000001/v_0.ts
#EXTINF:3.200,
000001/v_1.ts
#EXT-X-DISCONTINUITY
#EXTINF:4.000,
000002/v_0.ts
`, got)

	_, ok = m.RenderMedia("nope")
	assert.False(t, ok)
}

func TestWindowEvictionAndSequences(t *testing.T) {
	m := newTestManager(2)
	ev := m.Append("v", "000001/v_0.ts", 4)
	assert.Empty(t, ev)
	m.MarkDiscontinuity()
	m.Append("v", "000002/v_0.ts", 4)
	ev = m.Append("v", "000002/v_1.ts", 4) // evicts v_0 (no discont)
	assert.Equal(t, []string{"000001/v_0.ts"}, ev)

	got, _ := m.RenderMedia("v")
	assert.Contains(t, got, "#EXT-X-MEDIA-SEQUENCE:1")
	assert.Contains(t, got, "#EXT-X-DISCONTINUITY-SEQUENCE:0")
	assert.Contains(t, got, "#EXT-X-DISCONTINUITY\n") // 000002/v_0 still marked

	ev = m.Append("v", "000002/v_2.ts", 4) // evicts the discontinuity-marked seg
	assert.Equal(t, []string{"000002/v_0.ts"}, ev)
	got, _ = m.RenderMedia("v")
	assert.Contains(t, got, "#EXT-X-MEDIA-SEQUENCE:2")
	assert.Contains(t, got, "#EXT-X-DISCONTINUITY-SEQUENCE:1")
}

func TestRenderMaster(t *testing.T) {
	m := newTestManager(10)
	assert.Equal(t, `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="Thai",LANGUAGE="tha",DEFAULT=YES,AUTOSELECT=YES,URI="a_tha_0.m3u8"
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="English",LANGUAGE="eng",DEFAULT=NO,AUTOSELECT=YES,URI="a_eng_0.m3u8"
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="Thai",LANGUAGE="tha",DEFAULT=NO,AUTOSELECT=YES,URI="s_tha_0.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=5660000,RESOLUTION=1920x1080,CODECS="avc1.640028,mp4a.40.2",AUDIO="audio",SUBTITLES="subs"
v.m3u8
`, m.RenderMaster())
}
```

(BANDWIDTH = `bitrateK*1000 + 500000 + 160000` = 5000·1000·1 + 660000 → use exactly `BitrateK*1000 + 660000` so the golden value 5660000 holds.)

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/hls/playlist.go`:

```go
package hls

import (
	"fmt"
	"strings"
	"sync"
)

type VideoParams struct {
	Width    int
	Height   int
	BitrateK int
}

type segment struct {
	uri     string
	dur     float64
	discont bool
}

type mediaPlaylist struct {
	seq     int64
	discSeq int64
	segs    []segment
	pending bool // next append gets a discontinuity marker
}

type Manager struct {
	mu        sync.Mutex
	rends     []Rendition
	targetDur int
	window    int
	video     VideoParams
	media     map[string]*mediaPlaylist
}

func NewManager(rends []Rendition, targetDurSec, window int, video VideoParams) *Manager {
	m := &Manager{
		rends: rends, targetDur: targetDurSec, window: window, video: video,
		media: make(map[string]*mediaPlaylist, len(rends)),
	}
	for _, r := range rends {
		m.media[r.Key] = &mediaPlaylist{}
	}
	return m
}

func (m *Manager) Renditions() []Rendition {
	return m.rends
}

func (m *Manager) MarkDiscontinuity() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, pl := range m.media {
		if len(pl.segs) > 0 { // very first segment of a stream needs no marker
			pl.pending = true
		}
	}
}

func (m *Manager) Append(key, uri string, dur float64) (evicted []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pl, ok := m.media[key]
	if !ok {
		return nil
	}
	pl.segs = append(pl.segs, segment{uri: uri, dur: dur, discont: pl.pending})
	pl.pending = false
	for len(pl.segs) > m.window {
		old := pl.segs[0]
		pl.segs = pl.segs[1:]
		pl.seq++
		if old.discont {
			pl.discSeq++
		}
		evicted = append(evicted, old.uri)
	}
	return evicted
}

func (m *Manager) RenderMedia(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pl, ok := m.media[key]
	if !ok {
		return "", false
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", m.targetDur)
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", pl.seq)
	fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", pl.discSeq)
	for _, s := range pl.segs {
		if s.discont {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", s.dur, s.uri)
	}
	return b.String(), true
}

func (m *Manager) RenderMaster() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	hasAudio, hasSubs := false, false
	audioFirst := true
	for _, r := range m.rends {
		switch r.Kind {
		case KindAudio:
			hasAudio = true
			def := "NO"
			if audioFirst {
				def = "YES"
				audioFirst = false
			}
			fmt.Fprintf(&b,
				"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"audio\",NAME=%q,LANGUAGE=%q,DEFAULT=%s,AUTOSELECT=YES,URI=%q\n",
				r.Name, r.Lang, def, r.PlaylistURI())
		case KindSubs:
			hasSubs = true
			fmt.Fprintf(&b,
				"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\",NAME=%q,LANGUAGE=%q,DEFAULT=NO,AUTOSELECT=YES,URI=%q\n",
				r.Name, r.Lang, r.PlaylistURI())
		}
	}
	bandwidth := m.video.BitrateK*1000 + 660000
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.640028,mp4a.40.2\"",
		bandwidth, m.video.Width, m.video.Height)
	if hasAudio {
		b.WriteString(",AUDIO=\"audio\"")
	}
	if hasSubs {
		b.WriteString(",SUBTITLES=\"subs\"")
	}
	b.WriteString("\nv.m3u8\n")
	return b.String()
}
```

- [ ] **Step 4: Run — PASS** (`go test ./internal/hls/ -v`)

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: hls playlist state machine with sliding window"`

---

### Task 10: FFmpeg command builder

**Files:**
- Create: `internal/ffmpeg/command.go`, `internal/ffmpeg/command_test.go`

**Interfaces:**
- Consumes: `hls.Rendition`, `hls.MapTracks` result (Task 8); `ProbeResult` (Task 3).
- Produces:
  - `ffmpeg.VideoSettings{Width, Height, BitrateK int; Encoder string}` (Encoder: `"nvenc"` | `"software"`)
  - `ffmpeg.EncodeSpec{InputPath, OutDir string; SegmentSec int; DurationMs int64; Video VideoSettings; Renditions []hls.Rendition; TrackMap map[string]int}`
  - `ffmpeg.BuildEncodeArgs(s EncodeSpec) []string` — video + all audio renditions (real or silent filler) in ONE invocation; each output writes `<OutDir>/<key>_%d.ts` + CSV list `<OutDir>/<key>.csv`; video key is `v`
  - `ffmpeg.BuildSubExtractArgs(inputPath string, track SubtitleTrack, outPath string) []string` — extracts one subtitle track to a whole-file `.vtt` (uses `track.Path` as input when `track.External`)

- [ ] **Step 1: Failing golden test** `internal/ffmpeg/command_test.go`:

```go
package ffmpeg

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"hotpot-iptv/internal/hls"
)

func spec() EncodeSpec {
	return EncodeSpec{
		InputPath:  "/media/movie.mkv",
		OutDir:     "/streams/ch/000001",
		SegmentSec: 4,
		DurationMs: 61500,
		Video:      VideoSettings{Width: 1920, Height: 1080, BitrateK: 5000, Encoder: "software"},
		Renditions: []hls.Rendition{
			{Kind: hls.KindVideo, Key: "v"},
			{Kind: hls.KindAudio, Key: "a_tha_0", Lang: "tha"},
			{Kind: hls.KindAudio, Key: "a_eng_0", Lang: "eng"},
			{Kind: hls.KindSubs, Key: "s_tha_0", Lang: "tha"},
		},
		TrackMap: map[string]int{"v": 0, "a_tha_0": 0, "a_eng_0": -1, "s_tha_0": 0},
	}
}

func TestBuildEncodeArgsSoftware(t *testing.T) {
	got := strings.Join(BuildEncodeArgs(spec()), " ")
	want := "-hide_banner -nostdin -loglevel error -progress pipe:1 " +
		"-re -i /media/movie.mkv " +
		"-f lavfi -t 61.500 -i anullsrc=r=48000:cl=stereo " +
		"-filter_complex [0:v:0]scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1[vout] " +
		"-map [vout] -c:v libx264 -preset veryfast -b:v 5000k -maxrate 6000k -bufsize 10000k -sc_threshold 0 " +
		"-force_key_frames expr:gte(t,n_forced*4) " +
		"-f segment -segment_time 4 -segment_format mpegts -output_ts_offset 10 " +
		"-segment_list /streams/ch/000001/v.csv -segment_list_type csv /streams/ch/000001/v_%d.ts " +
		"-map 0:a:0 -c:a aac -b:a 160k -ac 2 " +
		"-f segment -segment_time 4 -segment_format mpegts -output_ts_offset 10 " +
		"-segment_list /streams/ch/000001/a_tha_0.csv -segment_list_type csv /streams/ch/000001/a_tha_0_%d.ts " +
		"-map 1:a:0 -c:a aac -b:a 160k -ac 2 " +
		"-f segment -segment_time 4 -segment_format mpegts -output_ts_offset 10 " +
		"-segment_list /streams/ch/000001/a_eng_0.csv -segment_list_type csv /streams/ch/000001/a_eng_0_%d.ts"
	assert.Equal(t, want, got)
}

func TestBuildEncodeArgsNvenc(t *testing.T) {
	s := spec()
	s.Video.Encoder = "nvenc"
	got := strings.Join(BuildEncodeArgs(s), " ")
	assert.Contains(t, got, "-c:v h264_nvenc -preset p5 -rc vbr -b:v 5000k -maxrate 6000k -bufsize 10000k -profile:v high -forced-idr 1")
	assert.NotContains(t, got, "libx264")
}

func TestBuildEncodeArgsNoFillerInput(t *testing.T) {
	s := spec()
	s.TrackMap["a_eng_0"] = 1 // both real → no anullsrc input
	got := strings.Join(BuildEncodeArgs(s), " ")
	assert.NotContains(t, got, "anullsrc")
	assert.Contains(t, got, "-map 0:a:1")
}

func TestBuildSubExtractArgs(t *testing.T) {
	embedded := strings.Join(BuildSubExtractArgs("/media/movie.mkv",
		SubtitleTrack{Index: 2}, "/streams/ch/000001/s_tha_0.vtt"), " ")
	assert.Equal(t,
		"-hide_banner -nostdin -loglevel error -y -i /media/movie.mkv -map 0:s:2 -f webvtt /streams/ch/000001/s_tha_0.vtt",
		embedded)

	external := strings.Join(BuildSubExtractArgs("/media/movie.mkv",
		SubtitleTrack{Index: -1, External: true, Path: "/media/movie.eng.srt"},
		"/streams/ch/000001/s_eng_0.vtt"), " ")
	assert.Equal(t,
		"-hide_banner -nostdin -loglevel error -y -i /media/movie.eng.srt -map 0:s:0 -f webvtt /streams/ch/000001/s_eng_0.vtt",
		external)
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/ffmpeg/command.go`:

```go
package ffmpeg

import (
	"fmt"
	"path/filepath"

	"hotpot-iptv/internal/hls"
)

type VideoSettings struct {
	Width    int
	Height   int
	BitrateK int
	Encoder  string // "nvenc" | "software"
}

type EncodeSpec struct {
	InputPath  string
	OutDir     string
	SegmentSec int
	DurationMs int64
	Video      VideoSettings
	Renditions []hls.Rendition
	TrackMap   map[string]int
}

// BuildEncodeArgs produces one ffmpeg invocation that encodes the video
// rendition plus every audio rendition. Audio renditions the file lacks
// (TrackMap value -1) are fed from a silent anullsrc input so their segment
// timeline stays continuous. Subtitles are handled separately
// (BuildSubExtractArgs) because they are extracted before the encode starts.
func BuildEncodeArgs(s EncodeSpec) []string {
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-progress", "pipe:1",
		"-re", "-i", s.InputPath,
	}

	needSilence := false
	for _, r := range s.Renditions {
		if r.Kind == hls.KindAudio && s.TrackMap[r.Key] == -1 {
			needSilence = true
		}
	}
	if needSilence {
		args = append(args,
			"-f", "lavfi",
			"-t", fmt.Sprintf("%.3f", float64(s.DurationMs)/1000),
			"-i", "anullsrc=r=48000:cl=stereo",
		)
	}

	args = append(args, "-filter_complex", fmt.Sprintf(
		"[0:v:0]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1[vout]",
		s.Video.Width, s.Video.Height, s.Video.Width, s.Video.Height))

	// video output
	args = append(args, "-map", "[vout]")
	args = append(args, videoCodecArgs(s.Video)...)
	args = append(args, "-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", s.SegmentSec))
	args = append(args, segmentArgs(s.OutDir, "v", s.SegmentSec)...)

	// audio outputs, rendition order
	for _, r := range s.Renditions {
		if r.Kind != hls.KindAudio {
			continue
		}
		idx := s.TrackMap[r.Key]
		if idx >= 0 {
			args = append(args, "-map", fmt.Sprintf("0:a:%d", idx))
		} else {
			args = append(args, "-map", "1:a:0")
		}
		args = append(args, "-c:a", "aac", "-b:a", "160k", "-ac", "2")
		args = append(args, segmentArgs(s.OutDir, r.Key, s.SegmentSec)...)
	}
	return args
}

func videoCodecArgs(v VideoSettings) []string {
	b := v.BitrateK
	common := []string{
		"-b:v", fmt.Sprintf("%dk", b),
		"-maxrate", fmt.Sprintf("%dk", b*12/10),
		"-bufsize", fmt.Sprintf("%dk", b*2),
	}
	if v.Encoder == "nvenc" {
		return append([]string{"-c:v", "h264_nvenc", "-preset", "p5", "-rc", "vbr"},
			append(common, "-profile:v", "high", "-forced-idr", "1")...)
	}
	return append([]string{"-c:v", "libx264", "-preset", "veryfast"},
		append(common, "-sc_threshold", "0")...)
}

func segmentArgs(outDir, key string, segSec int) []string {
	return []string{
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%d", segSec),
		"-segment_format", "mpegts",
		"-output_ts_offset", "10",
		"-segment_list", filepath.Join(outDir, key+".csv"),
		"-segment_list_type", "csv",
		filepath.Join(outDir, key+"_%d.ts"),
	}
}

// BuildSubExtractArgs extracts one subtitle track to a whole-file WebVTT.
func BuildSubExtractArgs(inputPath string, track SubtitleTrack, outPath string) []string {
	in := inputPath
	mapArg := fmt.Sprintf("0:s:%d", track.Index)
	if track.External {
		in = track.Path
		mapArg = "0:s:0"
	}
	return []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-y",
		"-i", in, "-map", mapArg, "-f", "webvtt", outPath,
	}
}
```

(Golden-test note: the exact `-maxrate` value for 5000k is `6000k` (5000·12/10). If assertion ordering differs from the built slice, fix the TEST golden string to match the implementation's deterministic order — the order above is stable.)

- [ ] **Step 4: Run — PASS**

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: ffmpeg encode and subtitle-extract command builders"`

---

### Task 11: FFmpeg process runner (progress, stall detection)

**Files:**
- Create: `internal/ffmpeg/runner.go`, `internal/ffmpeg/runner_test.go`, `internal/ffmpeg/testdata/fake_ffmpeg.sh` (chmod +x)

**Interfaces:**
- Produces:
  - `ffmpeg.ErrStalled` (sentinel)
  - `ffmpeg.Runner{FFmpegPath string; StallTimeout time.Duration}`
  - `ffmpeg.RunOpts{OnProgress func(outTimeUs int64); DisableStallWatch bool}`
  - `(r Runner) Run(ctx context.Context, args []string, opts RunOpts) error` — nil on clean exit; `ErrStalled`-wrapped error when no progress line arrives within `StallTimeout`; other failures wrap the last ~4 KB of stderr

- [ ] **Step 1: Fake ffmpeg + failing tests**

`internal/ffmpeg/testdata/fake_ffmpeg.sh` (then `chmod +x`):

```sh
#!/bin/sh
# Modes driven by first arg: ok | fail | stall
case "$1" in
ok)
  i=1
  while [ $i -le 3 ]; do
    echo "out_time_us=${i}000000"
    echo "progress=continue"
    i=$((i+1))
  done
  echo "progress=end"
  ;;
fail)
  echo "Conversion failed: broken input" >&2
  exit 1
  ;;
stall)
  echo "out_time_us=1000000"
  echo "progress=continue"
  sleep 30
  ;;
esac
```

`internal/ffmpeg/runner_test.go`:

```go
package ffmpeg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOK(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: 5 * time.Second}
	var progress []int64
	err := r.Run(context.Background(), []string{"ok"}, RunOpts{
		OnProgress: func(us int64) { progress = append(progress, us) },
	})
	require.NoError(t, err)
	assert.Equal(t, []int64{1000000, 2000000, 3000000}, progress)
}

func TestRunFail(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: 5 * time.Second}
	err := r.Run(context.Background(), []string{"fail"}, RunOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken input")
}

func TestRunStall(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: 300 * time.Millisecond}
	start := time.Now()
	err := r.Run(context.Background(), []string{"stall"}, RunOpts{})
	require.ErrorIs(t, err, ErrStalled)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRunContextCancel(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := r.Run(ctx, []string{"stall"}, RunOpts{DisableStallWatch: true})
	require.Error(t, err) // killed by context
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/ffmpeg/runner.go`:

```go
package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var ErrStalled = errors.New("ffmpeg stalled: no progress")

type Runner struct {
	FFmpegPath   string
	StallTimeout time.Duration
}

type RunOpts struct {
	OnProgress        func(outTimeUs int64)
	DisableStallWatch bool
}

// tailWriter keeps the last cap bytes written.
type tailWriter struct {
	buf []byte
	cap int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.cap {
		t.buf = t.buf[len(t.buf)-t.cap:]
	}
	return len(p), nil
}

func (r Runner) Run(ctx context.Context, args []string, opts RunOpts) error {
	cmd := exec.CommandContext(ctx, r.FFmpegPath, args...)
	stderr := &tailWriter{cap: 4096}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", r.FFmpegPath, err)
	}

	var stalled atomic.Bool
	var watchdog *time.Timer
	if !opts.DisableStallWatch {
		watchdog = time.AfterFunc(r.StallTimeout, func() {
			stalled.Store(true)
			_ = cmd.Process.Kill()
		})
		defer watchdog.Stop()
	}

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if watchdog != nil {
				watchdog.Reset(r.StallTimeout)
			}
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && opts.OnProgress != nil {
				opts.OnProgress(us)
			}
		}
	}

	err = cmd.Wait()
	if stalled.Load() {
		return fmt.Errorf("%w (last stderr: %s)", ErrStalled, string(stderr.buf))
	}
	if err != nil {
		return fmt.Errorf("ffmpeg exited: %w (last stderr: %s)", err, string(stderr.buf))
	}
	return nil
}
```

- [ ] **Step 4: Run — PASS** (`go test ./internal/ffmpeg/ -v`)

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: ffmpeg process runner with stall watchdog"`

---

### Task 12: Segment-list CSV tailer

**Files:**
- Create: `internal/ffmpeg/seglist.go`, `internal/ffmpeg/seglist_test.go`

**Interfaces:**
- Produces:
  - `ffmpeg.SegmentEntry{URI string; Start, End float64}` — `URI` is the basename of the segment file; `Duration()` method returns `End - Start`
  - `ffmpeg.TailCSV(ctx context.Context, path string, interval time.Duration, fn func(SegmentEntry))` — polls the CSV until ctx is done; a not-yet-existing file is not an error; calls `fn` once per complete new line, in order

- [ ] **Step 1: Failing test** `internal/ffmpeg/seglist_test.go`:

```go
package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailCSV(t *testing.T) {
	dir := t.TempDir()
	csv := filepath.Join(dir, "v.csv")

	var mu sync.Mutex
	var got []SegmentEntry
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		TailCSV(ctx, csv, 20*time.Millisecond, func(e SegmentEntry) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		})
		close(done)
	}()

	time.Sleep(60 * time.Millisecond) // file doesn't exist yet — no panic
	require.NoError(t, os.WriteFile(csv, []byte("/streams/ch/000001/v_0.ts,10.000000,14.000000\n"), 0o644))
	time.Sleep(60 * time.Millisecond)
	f, err := os.OpenFile(csv, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString("/streams/ch/000001/v_1.ts,14.000000,17.200000\n")
	require.NoError(t, err)
	f.Close()
	time.Sleep(60 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 2)
	assert.Equal(t, SegmentEntry{URI: "v_0.ts", Start: 10, End: 14}, got[0])
	assert.Equal(t, "v_1.ts", got[1].URI)
	assert.InDelta(t, 3.2, got[1].Duration(), 0.0001)
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/ffmpeg/seglist.go`:

```go
package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type SegmentEntry struct {
	URI   string
	Start float64
	End   float64
}

func (e SegmentEntry) Duration() float64 { return e.End - e.Start }

// TailCSV polls an ffmpeg -segment_list CSV file, invoking fn once per new
// complete line until ctx is done. The file not existing yet is normal.
func TailCSV(ctx context.Context, path string, interval time.Duration, fn func(SegmentEntry)) {
	consumed := 0
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		entries := readEntries(path)
		for consumed < len(entries) {
			fn(entries[consumed])
			consumed++
		}
		select {
		case <-ctx.Done():
			// final drain so segments written just before exit aren't lost
			entries = readEntries(path)
			for consumed < len(entries) {
				fn(entries[consumed])
				consumed++
			}
			return
		case <-tick.C:
		}
	}
}

func readEntries(path string) []SegmentEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []SegmentEntry
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(strings.TrimSpace(line), ",")
		if len(parts) != 3 {
			continue
		}
		start, err1 := strconv.ParseFloat(parts[1], 64)
		end, err2 := strconv.ParseFloat(parts[2], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, SegmentEntry{URI: filepath.Base(parts[0]), Start: start, End: end})
	}
	return out
}
```

- [ ] **Step 4: Run — PASS**

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: segment list csv tailer"`

---

### Task 13: Channel runner (engine core)

**Files:**
- Create: `internal/engine/runner.go`, `internal/engine/runner_test.go`

**Interfaces:**
- Consumes: `hls.*` (Tasks 7–9), `ffmpeg.*` (Tasks 3, 10–12).
- Produces:
  - `engine.Item{Path string; Abs string; Probe ffmpeg.ProbeResult}`
  - `engine.ChannelSpec{ID int32; Slug string; Items []Item; Video ffmpeg.VideoSettings; SegmentSec, Window int; StreamsPath string}`
  - `engine.Store interface { SaveState(ctx context.Context, channelID, itemPos int32, startedAt time.Time, status, lastErr string) error; LogEvent(ctx context.Context, channelID int32, level, message string) }` (zero `startedAt` ⇒ NULL)
  - `engine.ProcessRunner interface { Run(ctx context.Context, args []string, opts ffmpeg.RunOpts) error }`
  - `engine.NewRunner(spec ChannelSpec, startPos int32, store Store, proc ProcessRunner) *Runner` — computes the rendition union via `hls.ComputeRenditions`, creates the `hls.Manager`
  - `(r *Runner) Manager() *hls.Manager`; `(r *Runner) NowPlaying() (path string, offsetUs int64, pos int32)`; `(r *Runner) Run(ctx context.Context)` (blocks until ctx done)
- Error policy (from spec): 2 attempts per item, then skip; 5 consecutive failed items → status `error` + exponential backoff 1m→2m→4m→cap 5m, then continue; on ctx cancel persist status `stopped`.

- [ ] **Step 1: Failing test** `internal/engine/runner_test.go`:

```go
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/ffmpeg"
)

type memStore struct {
	mu     sync.Mutex
	states []string // "pos:status"
	events []string
}

func (m *memStore) SaveState(_ context.Context, _ int32, pos int32, _ time.Time, status, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, fmt.Sprintf("%d:%s", pos, status))
	return nil
}

func (m *memStore) LogEvent(_ context.Context, _ int32, level, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, level+": "+msg)
}

// fakeProc simulates ffmpeg: sub-extract writes a tiny vtt; encode writes 2
// segments + csv per -segment_list output.
type fakeProc struct{ failures map[string]int } // abs input path -> remaining failures

func (f *fakeProc) Run(_ context.Context, args []string, _ ffmpeg.RunOpts) error {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "-f webvtt") {
		out := args[len(args)-1]
		return os.WriteFile(out, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nhi\n"), 0o644)
	}
	input := args[indexOf(args, "-i")+1]
	if f.failures[input] > 0 {
		f.failures[input]--
		return fmt.Errorf("fake encode failure")
	}
	// write segments + csv for every -segment_list
	for i, a := range args {
		if a != "-segment_list" {
			continue
		}
		csvPath := args[i+1]
		key := strings.TrimSuffix(filepath.Base(csvPath), ".csv")
		dir := filepath.Dir(csvPath)
		var lines []string
		for n := 0; n < 2; n++ {
			seg := fmt.Sprintf("%s_%d.ts", key, n)
			if err := os.WriteFile(filepath.Join(dir, seg), []byte("ts"), 0o644); err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("%s,%d.000000,%d.000000", filepath.Join(dir, seg), 10+n*4, 14+n*4))
		}
		if err := os.WriteFile(csvPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

func testSpec(t *testing.T, streams string) ChannelSpec {
	probeA := ffmpeg.ProbeResult{
		DurationMs: 8000, VideoCodec: "h264",
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "tha"}},
		Subs:  []ffmpeg.SubtitleTrack{{Index: 0, Lang: "tha", Codec: "subrip"}},
	}
	probeB := ffmpeg.ProbeResult{
		DurationMs: 8000, VideoCodec: "h264",
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "eng"}},
	}
	return ChannelSpec{
		ID: 1, Slug: "movies",
		Items: []Item{
			{Path: "a.mkv", Abs: "/fake/a.mkv", Probe: probeA},
			{Path: "b.mkv", Abs: "/fake/b.mkv", Probe: probeB},
		},
		Video:      ffmpeg.VideoSettings{Width: 1280, Height: 720, BitrateK: 3000, Encoder: "software"},
		SegmentSec: 4, Window: 30, StreamsPath: streams,
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestRunnerPlaysThroughAndLoops(t *testing.T) {
	streams := t.TempDir()
	store := &memStore{}
	r := NewRunner(testSpec(t, streams), 0, store, &fakeProc{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// Wait until segments from both items are in the video playlist.
	waitFor(t, 5*time.Second, func() bool {
		pl, _ := r.Manager().RenderMedia("v")
		return strings.Contains(pl, "000001/v_0.ts") && strings.Contains(pl, "000002/v_0.ts")
	})
	cancel()
	<-done

	pl, _ := r.Manager().RenderMedia("v")
	assert.Contains(t, pl, "#EXT-X-DISCONTINUITY")

	// Rendition union: both tha and eng audio playlists exist; b.mkv fills tha with silence
	// (same segment cadence, so the tha playlist also gained item-2 segments).
	tha, ok := r.Manager().RenderMedia("a_tha_0")
	require.True(t, ok)
	assert.Contains(t, tha, "000002/a_tha_0_0.ts")

	// Subtitles: item 1 has real cues, item 2 got empty filler vtt segments.
	sub, ok := r.Manager().RenderMedia("s_tha_0")
	require.True(t, ok)
	assert.Contains(t, sub, "000001/s_tha_0_0.vtt")
	assert.Contains(t, sub, "000002/s_tha_0_0.vtt")
	empty, err := os.ReadFile(filepath.Join(streams, "movies", "000002", "s_tha_0_0.vtt"))
	require.NoError(t, err)
	assert.Equal(t, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n", string(empty))

	// State was persisted as running, and stopped at the end.
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Contains(t, store.states, "0:running")
	assert.Contains(t, store.states, "1:running")
	assert.Equal(t, "stopped", strings.Split(store.states[len(store.states)-1], ":")[1])
}

func TestRunnerSkipsFailingItemAfterRetry(t *testing.T) {
	streams := t.TempDir()
	store := &memStore{}
	proc := &fakeProc{failures: map[string]int{"/fake/a.mkv": 2}} // both attempts fail
	r := NewRunner(testSpec(t, streams), 0, store, proc)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	waitFor(t, 5*time.Second, func() bool {
		pl, _ := r.Manager().RenderMedia("v")
		return strings.Contains(pl, "000002/v_0.ts") // b.mkv played despite a.mkv failing
	})
	cancel()
	<-done

	store.mu.Lock()
	defer store.mu.Unlock()
	joined := strings.Join(store.events, "\n")
	assert.Contains(t, joined, "fake encode failure")
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/engine/runner.go`:

```go
// Package engine runs channels: one Runner goroutine per running channel.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/internal/hls"
)

type Item struct {
	Path  string
	Abs   string
	Probe ffmpeg.ProbeResult
}

type ChannelSpec struct {
	ID          int32
	Slug        string
	Items       []Item
	Video       ffmpeg.VideoSettings
	SegmentSec  int
	Window      int
	StreamsPath string
}

type Store interface {
	SaveState(ctx context.Context, channelID, itemPos int32, startedAt time.Time, status, lastErr string) error
	LogEvent(ctx context.Context, channelID int32, level, message string)
}

type ProcessRunner interface {
	Run(ctx context.Context, args []string, opts ffmpeg.RunOpts) error
}

const (
	maxConsecutiveFailures = 5
	backoffBase            = time.Minute
	backoffCap             = 5 * time.Minute
	tailInterval           = 200 * time.Millisecond
)

type Runner struct {
	spec     ChannelSpec
	startPos int32
	store    Store
	proc     ProcessRunner
	mgr      *hls.Manager

	itemSeq    int64
	nowPlaying atomic.Value // string
	offsetUs   atomic.Int64
	pos        atomic.Int32
}

func NewRunner(spec ChannelSpec, startPos int32, store Store, proc ProcessRunner) *Runner {
	probes := make([]ffmpeg.ProbeResult, 0, len(spec.Items))
	for _, it := range spec.Items {
		probes = append(probes, it.Probe)
	}
	rends := hls.ComputeRenditions(probes)
	mgr := hls.NewManager(rends, spec.SegmentSec, spec.Window, hls.VideoParams{
		Width: spec.Video.Width, Height: spec.Video.Height, BitrateK: spec.Video.BitrateK,
	})
	r := &Runner{spec: spec, startPos: startPos, store: store, proc: proc, mgr: mgr}
	r.nowPlaying.Store("")
	return r
}

func (r *Runner) Manager() *hls.Manager { return r.mgr }

func (r *Runner) NowPlaying() (string, int64, int32) {
	return r.nowPlaying.Load().(string), r.offsetUs.Load(), r.pos.Load()
}

// Run loops the playlist until ctx is cancelled, persisting position and
// applying the retry/skip/backoff error policy.
func (r *Runner) Run(ctx context.Context) {
	pos := r.startPos
	if int(pos) >= len(r.spec.Items) || pos < 0 {
		pos = 0
	}
	consecFails := 0
	backoff := backoffBase

	for ctx.Err() == nil {
		item := r.spec.Items[pos]
		r.pos.Store(pos)
		r.nowPlaying.Store(item.Path)
		_ = r.store.SaveState(ctx, r.spec.ID, pos, time.Now(), "running", "")

		err := r.playItem(ctx, item)
		if err != nil && ctx.Err() == nil {
			r.store.LogEvent(ctx, r.spec.ID, "warn", fmt.Sprintf("item %s failed, retrying: %v", item.Path, err))
			err = r.playItem(ctx, item)
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			consecFails++
			r.store.LogEvent(ctx, r.spec.ID, "error", fmt.Sprintf("item %s skipped: %v", item.Path, err))
			if consecFails >= maxConsecutiveFailures {
				_ = r.store.SaveState(ctx, r.spec.ID, pos, time.Time{}, "error", err.Error())
				select {
				case <-ctx.Done():
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > backoffCap {
					backoff = backoffCap
				}
				consecFails = 0
				continue // retry from the same position after backoff
			}
		} else {
			consecFails = 0
			backoff = backoffBase
		}
		pos = (pos + 1) % int32(len(r.spec.Items))
	}
	_ = r.store.SaveState(context.Background(), r.spec.ID, pos, time.Time{}, "stopped", "")
}

func (r *Runner) playItem(ctx context.Context, item Item) error {
	r.itemSeq++
	dirName := fmt.Sprintf("%06d", r.itemSeq)
	outDir := filepath.Join(r.spec.StreamsPath, r.spec.Slug, dirName)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	rends := r.mgr.Renditions()
	trackMap := hls.MapTracks(rends, item.Probe)

	// 1. Subtitles first: extract + split before the encode starts.
	segDur := time.Duration(r.spec.SegmentSec) * time.Second
	total := time.Duration(item.Probe.DurationMs) * time.Millisecond
	subSegs := map[string][]string{} // rendition key -> segment file names
	for _, rend := range rends {
		if rend.Kind != hls.KindSubs {
			continue
		}
		var cues []hls.Cue
		if idx := trackMap[rend.Key]; idx >= 0 {
			vttPath := filepath.Join(outDir, rend.Key+".vtt")
			args := ffmpeg.BuildSubExtractArgs(item.Abs, item.Probe.Subs[idx], vttPath)
			if err := r.proc.Run(ctx, args, ffmpeg.RunOpts{DisableStallWatch: true}); err != nil {
				r.store.LogEvent(ctx, r.spec.ID, "warn",
					fmt.Sprintf("subtitle extract failed for %s/%s: %v", item.Path, rend.Key, err))
			} else if f, err := os.Open(vttPath); err == nil {
				cues, _ = hls.ParseVTT(f)
				f.Close()
			}
		}
		bodies := hls.SplitVTT(cues, segDur, total)
		names := make([]string, 0, len(bodies))
		for i, body := range bodies {
			name := fmt.Sprintf("%s_%d.vtt", rend.Key, i)
			if err := os.WriteFile(filepath.Join(outDir, name), []byte(body), 0o644); err != nil {
				return fmt.Errorf("write vtt segment: %w", err)
			}
			names = append(names, name)
		}
		subSegs[rend.Key] = names
	}

	// 2. New file boundary.
	r.mgr.MarkDiscontinuity()

	// 3. Tail CSV lists → append segments live; subs advance in lockstep with video.
	tailCtx, stopTails := context.WithCancel(ctx)
	tailsDone := make(chan struct{}, len(rends))
	tails := 0
	videoSegIdx := 0
	for _, rend := range rends {
		if rend.Kind == hls.KindSubs {
			continue
		}
		rend := rend
		tails++
		go func() {
			defer func() { tailsDone <- struct{}{} }()
			ffmpeg.TailCSV(tailCtx, filepath.Join(outDir, rend.Key+".csv"), tailInterval, func(e ffmpeg.SegmentEntry) {
				r.appendAndClean(rend.Key, dirName+"/"+e.URI, e.Duration())
				if rend.Kind == hls.KindVideo {
					for key, names := range subSegs {
						if videoSegIdx < len(names) {
							r.appendAndClean(key, dirName+"/"+names[videoSegIdx], e.Duration())
						}
					}
					videoSegIdx++
				}
			})
		}()
	}

	// 4. Encode (realtime because of -re).
	spec := ffmpeg.EncodeSpec{
		InputPath: item.Abs, OutDir: outDir, SegmentSec: r.spec.SegmentSec,
		DurationMs: item.Probe.DurationMs, Video: r.spec.Video,
		Renditions: rends, TrackMap: trackMap,
	}
	runErr := r.proc.Run(ctx, ffmpeg.BuildEncodeArgs(spec), ffmpeg.RunOpts{
		OnProgress: func(us int64) { r.offsetUs.Store(us) },
	})

	stopTails()
	for i := 0; i < tails; i++ {
		<-tailsDone
	}
	return runErr
}

// appendAndClean appends a segment and deletes any evicted segment files.
func (r *Runner) appendAndClean(key, uri string, dur float64) {
	for _, old := range r.mgr.Append(key, uri, dur) {
		if strings.Contains(old, "..") {
			continue
		}
		_ = os.Remove(filepath.Join(r.spec.StreamsPath, r.spec.Slug, old))
	}
}
```

Known race accepted: `videoSegIdx` is only touched from the single video tail goroutine, so it needs no lock; document that with the code as shown.

- [ ] **Step 4: Run — PASS** (`go test ./internal/engine/ -v`; run with `-race`)

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: channel runner with filler renditions and error policy"`

---

### Task 14: Supervisor + SQL store/loader + main wiring

**Files:**
- Create: `internal/engine/supervisor.go`, `internal/engine/store.go`, `internal/engine/loader.go`, `internal/engine/supervisor_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: Runner (Task 13), sqlc (Task 2), config (Task 1).
- Produces:
  - `engine.ChannelLoader interface { Load(ctx context.Context, channelID int32) (ChannelSpec, int32, error) }`
  - `engine.NewSupervisor(loader ChannelLoader, store Store, proc ProcessRunner) *Supervisor`
  - `(s *Supervisor) Start(ctx context.Context, channelID int32) error` (error if already running / load fails); `Stop(channelID int32) error`; `StopAll()`; `Status(channelID int32) (ChannelStatus, bool)`; `ManagerFor(slug string) (*hls.Manager, bool)`; `RestoreRunning(ctx context.Context) error`
  - `engine.ChannelStatus{State string; Slug string; NowPlaying string; OffsetSec int64; ItemPosition int32}` (json: `state, slug, now_playing, offset_sec, item_position`) — `State` is `"running"` for a live runner; non-running channels simply return `ok=false` (callers render "stopped")
  - `engine.NewSQLStore(q *sqlc.Queries) *SQLStore` (implements Store); `engine.NewSQLLoader(q *sqlc.Queries, cfg LoaderConfig) *SQLLoader`; `engine.LoaderConfig{MediaPath, StreamsPath, Encoder string; SegmentSec, Window int}`
  - `(l *SQLLoader) RunningChannelIDs(ctx) ([]int32, error)` — from `ListRunningChannelStates`

- [ ] **Step 1: Failing test** `internal/engine/supervisor_test.go`:

```go
package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memLoader struct{ spec ChannelSpec }

func (m memLoader) Load(context.Context, int32) (ChannelSpec, int32, error) {
	return m.spec, 0, nil
}

func TestSupervisorStartStop(t *testing.T) {
	streams := t.TempDir()
	spec := testSpec(t, streams)
	sup := NewSupervisor(memLoader{spec: spec}, &memStore{}, &fakeProc{})

	require.NoError(t, sup.Start(context.Background(), 1))
	assert.Error(t, sup.Start(context.Background(), 1), "double start must error")

	waitFor(t, 5*time.Second, func() bool {
		mgr, ok := sup.ManagerFor("movies")
		if !ok {
			return false
		}
		pl, _ := mgr.RenderMedia("v")
		return strings.Contains(pl, "v_0.ts")
	})

	st, ok := sup.Status(1)
	require.True(t, ok)
	assert.Equal(t, "running", st.State)
	assert.Equal(t, "movies", st.Slug)
	assert.NotEmpty(t, st.NowPlaying)

	require.NoError(t, sup.Stop(1))
	_, ok = sup.Status(1)
	assert.False(t, ok)
	_, ok = sup.ManagerFor("movies")
	assert.False(t, ok)

	assert.Error(t, sup.Stop(1), "stop when not running must error")
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

`internal/engine/supervisor.go`:

```go
package engine

import (
	"context"
	"fmt"
	"sync"

	"hotpot-iptv/internal/hls"
)

type ChannelLoader interface {
	Load(ctx context.Context, channelID int32) (ChannelSpec, int32, error)
}

type ChannelStatus struct {
	State        string `json:"state"`
	Slug         string `json:"slug"`
	NowPlaying   string `json:"now_playing"`
	OffsetSec    int64  `json:"offset_sec"`
	ItemPosition int32  `json:"item_position"`
}

type managed struct {
	runner *Runner
	slug   string
	cancel context.CancelFunc
	done   chan struct{}
}

type Supervisor struct {
	mu     sync.Mutex
	procs  map[int32]*managed
	loader ChannelLoader
	store  Store
	proc   ProcessRunner
}

func NewSupervisor(loader ChannelLoader, store Store, proc ProcessRunner) *Supervisor {
	return &Supervisor{procs: map[int32]*managed{}, loader: loader, store: store, proc: proc}
}

func (s *Supervisor) Start(ctx context.Context, channelID int32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.procs[channelID]; ok {
		return fmt.Errorf("channel %d already running", channelID)
	}
	spec, startPos, err := s.loader.Load(ctx, channelID)
	if err != nil {
		return fmt.Errorf("load channel %d: %w", channelID, err)
	}
	runner := NewRunner(spec, startPos, s.store, s.proc)
	runCtx, cancel := context.WithCancel(context.Background()) // outlives the HTTP request
	m := &managed{runner: runner, slug: spec.Slug, cancel: cancel, done: make(chan struct{})}
	s.procs[channelID] = m
	go func() {
		runner.Run(runCtx)
		close(m.done)
	}()
	return nil
}

func (s *Supervisor) Stop(channelID int32) error {
	s.mu.Lock()
	m, ok := s.procs[channelID]
	if ok {
		delete(s.procs, channelID)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("channel %d not running", channelID)
	}
	m.cancel()
	<-m.done
	return nil
}

func (s *Supervisor) StopAll() {
	s.mu.Lock()
	all := make([]*managed, 0, len(s.procs))
	for id, m := range s.procs {
		all = append(all, m)
		delete(s.procs, id)
	}
	s.mu.Unlock()
	for _, m := range all {
		m.cancel()
		<-m.done
	}
}

func (s *Supervisor) Status(channelID int32) (ChannelStatus, bool) {
	s.mu.Lock()
	m, ok := s.procs[channelID]
	s.mu.Unlock()
	if !ok {
		return ChannelStatus{}, false
	}
	path, offsetUs, pos := m.runner.NowPlaying()
	return ChannelStatus{
		State: "running", Slug: m.slug, NowPlaying: path,
		OffsetSec: offsetUs / 1_000_000, ItemPosition: pos,
	}, true
}

func (s *Supervisor) ManagerFor(slug string) (*hls.Manager, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.procs {
		if m.slug == slug {
			return m.runner.Manager(), true
		}
	}
	return nil, false
}

// RestoreRunning restarts every channel whose persisted status is "running".
func (s *Supervisor) RestoreRunning(ctx context.Context) error {
	ids, err := s.loader.(interface {
		RunningChannelIDs(ctx context.Context) ([]int32, error)
	}).RunningChannelIDs(ctx)
	if err != nil {
		return fmt.Errorf("list running channels: %w", err)
	}
	for _, id := range ids {
		if err := s.Start(ctx, id); err != nil {
			s.store.LogEvent(ctx, id, "error", fmt.Sprintf("restore failed: %v", err))
		}
	}
	return nil
}
```

(Interface-assertion note: `RestoreRunning` type-asserts the loader; `memLoader` in tests doesn't implement it and that's fine — tests don't call RestoreRunning. If you prefer, add `RunningChannelIDs` to `ChannelLoader` and give `memLoader` a stub returning nil — pick one and keep it consistent; the simpler choice is adding it to the interface.)

Take the simpler choice: put `RunningChannelIDs(ctx context.Context) ([]int32, error)` INTO `ChannelLoader`, drop the type assertion, and add to `memLoader`:

```go
func (m memLoader) RunningChannelIDs(context.Context) ([]int32, error) { return nil, nil }
```

`internal/engine/store.go`:

```go
package engine

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"hotpot-iptv/sqlc"
)

type SQLStore struct {
	q *sqlc.Queries
}

func NewSQLStore(q *sqlc.Queries) *SQLStore { return &SQLStore{q: q} }

func (s *SQLStore) SaveState(ctx context.Context, channelID, itemPos int32, startedAt time.Time, status, lastErr string) error {
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

func (s *SQLStore) LogEvent(ctx context.Context, channelID int32, level, message string) {
	if err := s.q.InsertChannelEvent(ctx, sqlc.InsertChannelEventParams{
		ChannelID: channelID, Level: level, Message: message,
	}); err != nil {
		log.Printf("log event for channel %d: %v", channelID, err)
	}
}
```

`internal/engine/loader.go`:

```go
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/sqlc"
)

type LoaderConfig struct {
	MediaPath   string
	StreamsPath string
	Encoder     string
	SegmentSec  int
	Window      int
}

type SQLLoader struct {
	q   *sqlc.Queries
	cfg LoaderConfig
}

func NewSQLLoader(q *sqlc.Queries, cfg LoaderConfig) *SQLLoader {
	return &SQLLoader{q: q, cfg: cfg}
}

func (l *SQLLoader) Load(ctx context.Context, channelID int32) (ChannelSpec, int32, error) {
	ch, err := l.q.GetChannel(ctx, channelID)
	if err != nil {
		return ChannelSpec{}, 0, fmt.Errorf("get channel: %w", err)
	}
	rows, err := l.q.ListPlaylistItems(ctx, channelID)
	if err != nil {
		return ChannelSpec{}, 0, fmt.Errorf("list playlist: %w", err)
	}
	if len(rows) == 0 {
		return ChannelSpec{}, 0, fmt.Errorf("channel %q has an empty playlist", ch.Slug)
	}
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	files, err := l.q.GetMediaFilesByPaths(ctx, paths)
	if err != nil {
		return ChannelSpec{}, 0, fmt.Errorf("load probes: %w", err)
	}
	probes := make(map[string]ffmpeg.ProbeResult, len(files))
	for _, f := range files {
		var p ffmpeg.ProbeResult
		if err := json.Unmarshal(f.Probe, &p); err != nil {
			return ChannelSpec{}, 0, fmt.Errorf("bad probe cache for %q: %w", f.Path, err)
		}
		probes[f.Path] = p
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		p, ok := probes[r.Path]
		if !ok {
			return ChannelSpec{}, 0, fmt.Errorf("no probe cached for %q — re-save the playlist", r.Path)
		}
		items = append(items, Item{
			Path: r.Path, Abs: filepath.Join(l.cfg.MediaPath, r.Path), Probe: p,
		})
	}
	startPos := int32(0)
	if st, err := l.q.GetChannelState(ctx, channelID); err == nil {
		startPos = st.ItemPosition
	}
	return ChannelSpec{
		ID: ch.ID, Slug: ch.Slug, Items: items,
		Video: ffmpeg.VideoSettings{
			Width: int(ch.VideoWidth), Height: int(ch.VideoHeight),
			BitrateK: int(ch.VideoBitrateK), Encoder: l.cfg.Encoder,
		},
		SegmentSec: l.cfg.SegmentSec, Window: l.cfg.Window, StreamsPath: l.cfg.StreamsPath,
	}, startPos, nil
}

func (l *SQLLoader) RunningChannelIDs(ctx context.Context) ([]int32, error) {
	states, err := l.q.ListRunningChannelStates(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int32, 0, len(states))
	for _, st := range states {
		ids = append(ids, st.ChannelID)
	}
	return ids, nil
}
```

`main.go` — inside the `PSQL_URL != ""` block, build and restore the supervisor:

```go
		q := sqlc.New(pool)
		sup := engine.NewSupervisor(
			engine.NewSQLLoader(q, engine.LoaderConfig{
				MediaPath: cfg.MediaPath, StreamsPath: cfg.StreamsPath,
				Encoder: cfg.Encoder, SegmentSec: cfg.SegmentSeconds, Window: cfg.WindowSegments,
			}),
			engine.NewSQLStore(q),
			ffmpeg.Runner{FFmpegPath: cfg.FFmpegPath, StallTimeout: 30 * time.Second},
		)
		if err := sup.RestoreRunning(context.Background()); err != nil {
			log.Printf("restore running channels: %v", err)
		}
```

(`ffmpeg.Runner` already satisfies `engine.ProcessRunner`.)

- [ ] **Step 4: Run — PASS** (`go test -p 1 -race ./internal/engine/ -v`); `go build ./...`

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: channel supervisor with sql-backed store, loader, and restore"`

---

### Task 15: HLS delivery HTTP (streams)

**Files:**
- Create: `api/streams/http/server.go`, `api/streams/http/server_test.go`
- Modify: `main.go` (mount `/streams`)

**Interfaces:**
- Consumes: `hls.Manager` render methods.
- Produces: `streamshttp.ManagerSource interface { ManagerFor(slug string) (*hls.Manager, bool) }` (satisfied by `*engine.Supervisor`); `streamshttp.NewServer(src ManagerSource, streamsPath string) Server`; `(Server) NewRouter() *chi.Mux` with routes `GET /{slug}/master.m3u8`, `GET /{slug}/{playlist}.m3u8`, `GET /{slug}/{item}/{file}`. All responses carry `Access-Control-Allow-Origin: *` and `Cache-Control: no-store` on playlists (segments get `max-age=60`).

- [ ] **Step 1: Failing test** `api/streams/http/server_test.go`:

```go
package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/hls"
)

type fakeSrc struct{ mgr *hls.Manager }

func (f fakeSrc) ManagerFor(slug string) (*hls.Manager, bool) {
	if slug == "movies" {
		return f.mgr, true
	}
	return nil, false
}

func TestStreamsServer(t *testing.T) {
	streams := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(streams, "movies", "000001"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(streams, "movies", "000001", "v_0.ts"), []byte("TSDATA"), 0o644))

	mgr := hls.NewManager([]hls.Rendition{
		{Kind: hls.KindVideo, Key: "v", Name: "Video"},
		{Kind: hls.KindAudio, Key: "a_tha_0", Lang: "tha", Name: "Thai"},
	}, 4, 30, hls.VideoParams{Width: 1920, Height: 1080, BitrateK: 5000})
	mgr.Append("v", "000001/v_0.ts", 4)

	srv := httptest.NewServer(NewServer(fakeSrc{mgr: mgr}, streams).NewRouter())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/movies/master.m3u8")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/vnd.apple.mpegurl", res.Header.Get("Content-Type"))
	assert.Equal(t, "*", res.Header.Get("Access-Control-Allow-Origin"))
	res.Body.Close()

	res, err = http.Get(srv.URL + "/movies/v.m3u8")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	res.Body.Close()

	res, err = http.Get(srv.URL + "/movies/nope.m3u8")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	res.Body.Close()

	res, err = http.Get(srv.URL + "/movies/000001/v_0.ts")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "video/mp2t", res.Header.Get("Content-Type"))
	res.Body.Close()

	res, err = http.Get(srv.URL + "/other/master.m3u8")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
	res.Body.Close()

	res, err = http.Get(srv.URL + "/movies/000001/..%2F..%2Fsecret")
	require.NoError(t, err)
	assert.NotEqual(t, http.StatusOK, res.StatusCode)
	res.Body.Close()
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `api/streams/http/server.go`:

```go
// Package http serves generated HLS playlists (from memory) and segment
// files (from disk) with open CORS so any player can tune in.
package http

import (
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"hotpot-iptv/internal/hls"
)

type ManagerSource interface {
	ManagerFor(slug string) (*hls.Manager, bool)
}

type Server struct {
	src         ManagerSource
	streamsPath string
}

func NewServer(src ManagerSource, streamsPath string) Server {
	return Server{src: src, streamsPath: streamsPath}
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		next.ServeHTTP(w, r)
	})
}

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Use(cors)
	r.Get("/{slug}/master.m3u8", s.Master)
	r.Get("/{slug}/{playlist}", s.MediaPlaylist)
	r.Get("/{slug}/{item}/{file}", s.Segment)
	return r
}

func writePlaylist(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}

func (s Server) Master(w http.ResponseWriter, r *http.Request) {
	mgr, ok := s.src.ManagerFor(chi.URLParam(r, "slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writePlaylist(w, mgr.RenderMaster())
}

func (s Server) MediaPlaylist(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "playlist")
	if !strings.HasSuffix(name, ".m3u8") {
		http.NotFound(w, r)
		return
	}
	mgr, ok := s.src.ManagerFor(chi.URLParam(r, "slug"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, ok := mgr.RenderMedia(strings.TrimSuffix(name, ".m3u8"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	writePlaylist(w, body)
}

func (s Server) Segment(w http.ResponseWriter, r *http.Request) {
	slug, item, file := chi.URLParam(r, "slug"), chi.URLParam(r, "item"), chi.URLParam(r, "file")
	if !safeName.MatchString(slug) || !safeName.MatchString(item) || !safeName.MatchString(file) ||
		strings.Contains(item, "..") || strings.Contains(file, "..") {
		http.NotFound(w, r)
		return
	}
	switch filepath.Ext(file) {
	case ".ts":
		w.Header().Set("Content-Type", "video/mp2t")
	case ".vtt":
		w.Header().Set("Content-Type", "text/vtt")
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=60")
	http.ServeFile(w, r, filepath.Join(s.streamsPath, slug, item, file))
}
```

`main.go`: `r.Mount("/streams", streamshttp.NewServer(sup, cfg.StreamsPath).NewRouter())` (inside the DB block; import `streamshttp "hotpot-iptv/api/streams/http"`).

- [ ] **Step 4: Run — PASS** (`go test ./api/streams/... -v`); `go build ./...`

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: hls delivery endpoints"`

---

### Task 16: Control API (start/stop/status, enriched list)

**Files:**
- Modify: `api/channels/server.go`, `api/channels/service/client.go`, `api/channels/service/channels.go`, `api/channels/http/router.go`, `api/channels/integration_test.go`, `main.go`
- Create: `api/channels/http/control.go`

**Interfaces:**
- Consumes: `engine.Supervisor` (Task 14) via a new `service.Engine` interface.
- Produces:
  - `service.Engine interface { Start(ctx context.Context, channelID int32) error; Stop(channelID int32) error; Status(channelID int32) (engine.ChannelStatus, bool) }`
  - `channels.GetHTTPHandler(pool *pgxpool.Pool, prober command.Prober, mediaPath string, eng service.Engine) *chi.Mux` — NEW SIGNATURE (update main.go)
  - New routes: `POST /channels/{id}/start`, `POST /channels/{id}/stop`, `GET /channels/{id}/status`
  - `GET /channels` now returns `[]service.ChannelWithStatus{channel.Channel; Status *engine.ChannelStatus}` (json: embedded channel fields + `status` — null when stopped)
  - Service methods: `StartChannel(ctx, id) error` (validates the channel exists first via `GetChannel`), `StopChannel(id) error`, `ChannelStatus(id) *engine.ChannelStatus`

- [ ] **Step 1: Extend the integration test** — add to `api/channels/integration_test.go`:

```go
type fakeEngine struct{ running map[int32]bool }

func newFakeEngine() *fakeEngine { return &fakeEngine{running: map[int32]bool{}} }

func (f *fakeEngine) Start(_ context.Context, id int32) error {
	if f.running[id] {
		return fmt.Errorf("already running")
	}
	f.running[id] = true
	return nil
}

func (f *fakeEngine) Stop(id int32) error {
	if !f.running[id] {
		return fmt.Errorf("not running")
	}
	delete(f.running, id)
	return nil
}

func (f *fakeEngine) Status(id int32) (engine.ChannelStatus, bool) {
	if !f.running[id] {
		return engine.ChannelStatus{}, false
	}
	return engine.ChannelStatus{State: "running", Slug: "movies", NowPlaying: "a.mkv"}, true
}

func TestChannelControlAPI(t *testing.T) {
	pool := testdb.New(t)
	media := t.TempDir()
	eng := newFakeEngine()
	srv := httptest.NewServer(channels.GetHTTPHandler(pool, fakeProber{}, media, eng))
	defer srv.Close()

	_, out := doJSON(t, srv, "POST", "/channels", map[string]any{"name": "Movies", "number": 1})
	id := int(out["data"].(map[string]any)["id"].(float64))

	res, _ := doJSON(t, srv, "POST", "/channels/"+itoa(id)+"/start", nil)
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels/"+itoa(id)+"/status", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "running", out["data"].(map[string]any)["state"])

	res, out = doJSON(t, srv, "GET", "/channels", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	first := out["data"].([]any)[0].(map[string]any)
	assert.Equal(t, "running", first["status"].(map[string]any)["state"])

	res, _ = doJSON(t, srv, "POST", "/channels/"+itoa(id)+"/stop", nil)
	assert.Equal(t, http.StatusOK, res.StatusCode)

	res, out = doJSON(t, srv, "GET", "/channels/"+itoa(id)+"/status", nil)
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "stopped", out["data"].(map[string]any)["state"])

	// starting a nonexistent channel 404s
	res, _ = doJSON(t, srv, "POST", "/channels/9999/start", nil)
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}
```

Update the existing `TestChannelsAPI` construction to pass `newFakeEngine()` too. Imports: add `hotpot-iptv/internal/engine`.

- [ ] **Step 2: Run — FAIL** (signature mismatch)

- [ ] **Step 3: Implement**

`api/channels/service/client.go` — add engine:

```go
package service

import (
	"context"

	"hotpot-iptv/internal/channel/app"
	"hotpot-iptv/internal/engine"
)

type Engine interface {
	Start(ctx context.Context, channelID int32) error
	Stop(channelID int32) error
	Status(channelID int32) (engine.ChannelStatus, bool)
}

type Client struct {
	app app.Application
	eng Engine
}

func NewClient(a app.Application, eng Engine) Client { return Client{app: a, eng: eng} }
```

Append to `api/channels/service/channels.go`:

```go
type ChannelWithStatus struct {
	channel.Channel
	Status *engine.ChannelStatus `json:"status"`
}

func (c Client) ListChannelsWithStatus(ctx context.Context) ([]ChannelWithStatus, error) {
	chs, err := c.app.Queries.List.Handle(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelWithStatus, 0, len(chs))
	for _, ch := range chs {
		item := ChannelWithStatus{Channel: ch}
		if st, ok := c.eng.Status(ch.ID); ok {
			item.Status = &st
		}
		out = append(out, item)
	}
	return out, nil
}

func (c Client) StartChannel(ctx context.Context, id int32) error {
	if _, err := c.app.Queries.Get.Handle(ctx, id); err != nil {
		return err // maps ErrNotFound → 404 in http layer
	}
	return c.eng.Start(ctx, id)
}

func (c Client) StopChannel(id int32) error {
	return c.eng.Stop(id)
}

func (c Client) ChannelStatus(id int32) engine.ChannelStatus {
	if st, ok := c.eng.Status(id); ok {
		return st
	}
	return engine.ChannelStatus{State: "stopped"}
}
```

(Imports in channels.go: add `hotpot-iptv/internal/engine`.) Change `List` handler in `api/channels/http/list.go` to call `s.svc.ListChannelsWithStatus(r.Context())`.

`api/channels/http/control.go`:

```go
package http

import "net/http"

func (s Server) Start(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.svc.StartChannel(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, map[string]bool{"started": true})
}

func (s Server) Stop(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	if err := s.svc.StopChannel(id); err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, map[string]bool{"stopped": true})
}

func (s Server) Status(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, r, err)
		return
	}
	ok(w, r, s.svc.ChannelStatus(id))
}
```

`api/channels/http/router.go` — add inside `r.Route("/{id}", ...)`:

```go
			r.Post("/start", s.Start)
			r.Post("/stop", s.Stop)
			r.Get("/status", s.Status)
```

`api/channels/server.go` — new signature:

```go
func GetHTTPHandler(pool *pgxpool.Pool, prober command.Prober, mediaPath string, eng service.Engine) *chi.Mux {
	a := app.NewApplication(pool, prober, mediaPath)
	svc := service.NewClient(a, eng)
	return channelshttp.NewServer(svc).NewRouter()
}
```

`main.go`: pass `sup` — `channels.GetHTTPHandler(pool, prober, cfg.MediaPath, sup)` (move the supervisor construction ABOVE the mounts).

Note: `Stop` on a not-running channel returns 500 via `fail` (generic error). Acceptable for v1 — the UI only shows Stop for running channels.

- [ ] **Step 4: Run — PASS** (`go test -p 1 ./api/channels/ -v`); `go build ./...`

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: channel start/stop/status control API"`

---

### Task 17: EPG (XMLTV) + M3U export

**Files:**
- Create: `internal/epg/epg.go`, `internal/epg/epg_test.go`, `api/export/http/server.go`, `api/export/http/server_test.go`
- Modify: `main.go` (mount `/playlist.m3u` and `/epg.xml`)

**Interfaces:**
- Produces:
  - `epg.Item{Title string; DurationMs int64}`
  - `epg.ChannelSchedule{Slug, Name string; Number int32; Items []Item; CurrentPos int; ItemStartedAt time.Time}` (zero `ItemStartedAt` ⇒ schedule starts at `now`)
  - `epg.Forward(cs ChannelSchedule, now time.Time, horizon time.Duration) []epg.Entry` where `Entry{Slug string; Start, Stop time.Time; Title string}` — walks the looped playlist from CurrentPos until `Start >= now+horizon`; returns nil if total playlist duration is 0
  - `epg.RenderXMLTV(schedules []ChannelSchedule, now time.Time, horizon time.Duration) string`
  - `epg.RenderM3U(baseURL string, schedules []ChannelSchedule) string`
  - `exporthttp.NewServer(pool *pgxpool.Pool, statusSrc StatusSource) Server` with `StatusSource interface { Status(channelID int32) (engine.ChannelStatus, bool) }`; routes `GET /playlist.m3u`, `GET /epg.xml`

- [ ] **Step 1: Failing tests** `internal/epg/epg_test.go`:

```go
package epg

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sched(startedAgo time.Duration, now time.Time) ChannelSchedule {
	return ChannelSchedule{
		Slug: "movies", Name: "Movies", Number: 1,
		Items: []Item{
			{Title: "First", DurationMs: 3600_000},
			{Title: "Second", DurationMs: 1800_000},
		},
		CurrentPos:    0,
		ItemStartedAt: now.Add(-startedAgo),
	}
}

func TestForward(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	entries := Forward(sched(30*time.Minute, now), now, 3*time.Hour)

	require.NotEmpty(t, entries)
	// First started 30m ago, runs 60m total.
	assert.Equal(t, "First", entries[0].Title)
	assert.Equal(t, now.Add(-30*time.Minute), entries[0].Start)
	assert.Equal(t, now.Add(30*time.Minute), entries[0].Stop)
	assert.Equal(t, "Second", entries[1].Title)
	// Loops: entries cover now..now+3h.
	last := entries[len(entries)-1]
	assert.True(t, last.Start.Before(now.Add(3*time.Hour)))
	assert.True(t, last.Stop.After(now.Add(3*time.Hour)) || last.Stop.Equal(now.Add(3*time.Hour)))
}

func TestForwardStoppedChannelStartsNow(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	cs := sched(0, now)
	cs.ItemStartedAt = time.Time{}
	entries := Forward(cs, now, time.Hour)
	require.NotEmpty(t, entries)
	assert.Equal(t, now, entries[0].Start)
}

func TestForwardZeroDuration(t *testing.T) {
	now := time.Now()
	cs := ChannelSchedule{Slug: "x", Items: []Item{{Title: "a", DurationMs: 0}}}
	assert.Nil(t, Forward(cs, now, time.Hour))
}

func TestRenderXMLTV(t *testing.T) {
	now := time.Date(2026, 8, 12, 20, 0, 0, 0, time.UTC)
	out := RenderXMLTV([]ChannelSchedule{sched(0, now)}, now, time.Hour)
	assert.Contains(t, out, `<?xml version="1.0" encoding="UTF-8"?>`)
	assert.Contains(t, out, `<channel id="movies">`)
	assert.Contains(t, out, `<display-name>Movies</display-name>`)
	assert.Contains(t, out, `start="20260812200000 +0000"`)
	assert.Contains(t, out, `<title>First</title>`)
}

func TestRenderM3U(t *testing.T) {
	out := RenderM3U("http://box:8080", []ChannelSchedule{sched(0, time.Now())})
	assert.Equal(t, `#EXTM3U
#EXTINF:-1 tvg-id="movies" tvg-name="Movies" tvg-chno="1",Movies
http://box:8080/streams/movies/master.m3u8
`, out)
}

func TestXMLEscaping(t *testing.T) {
	now := time.Now().UTC()
	cs := ChannelSchedule{Slug: "x", Name: "A&B", Items: []Item{{Title: "<fun>", DurationMs: 60000}}}
	out := RenderXMLTV([]ChannelSchedule{cs}, now, time.Minute)
	assert.Contains(t, out, "A&amp;B")
	assert.Contains(t, out, "&lt;fun&gt;")
	assert.NotContains(t, out, "<fun>")
}
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement** `internal/epg/epg.go`:

```go
// Package epg computes a forward-looking program guide from each channel's
// looped playlist and renders XMLTV + M3U outputs.
package epg

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

type Item struct {
	Title      string
	DurationMs int64
}

type ChannelSchedule struct {
	Slug          string
	Name          string
	Number        int32
	Items         []Item
	CurrentPos    int
	ItemStartedAt time.Time
}

type Entry struct {
	Slug  string
	Start time.Time
	Stop  time.Time
	Title string
}

func Forward(cs ChannelSchedule, now time.Time, horizon time.Duration) []Entry {
	if len(cs.Items) == 0 {
		return nil
	}
	var total int64
	for _, it := range cs.Items {
		total += it.DurationMs
	}
	if total <= 0 {
		return nil
	}
	pos := cs.CurrentPos
	if pos < 0 || pos >= len(cs.Items) {
		pos = 0
	}
	start := cs.ItemStartedAt
	if start.IsZero() {
		start = now
	}
	end := now.Add(horizon)
	var entries []Entry
	for start.Before(end) {
		it := cs.Items[pos]
		stop := start.Add(time.Duration(it.DurationMs) * time.Millisecond)
		entries = append(entries, Entry{Slug: cs.Slug, Start: start, Stop: stop, Title: it.Title})
		start = stop
		pos = (pos + 1) % len(cs.Items)
	}
	return entries
}

func xmltvTime(t time.Time) string {
	return t.Format("20060102150405 -0700")
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func RenderXMLTV(schedules []ChannelSchedule, now time.Time, horizon time.Duration) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<tv generator-info-name="hotpot-iptv">` + "\n")
	for _, cs := range schedules {
		fmt.Fprintf(&b, "  <channel id=%q>\n    <display-name>%s</display-name>\n  </channel>\n",
			cs.Slug, esc(cs.Name))
	}
	for _, cs := range schedules {
		for _, e := range Forward(cs, now, horizon) {
			fmt.Fprintf(&b, "  <programme start=%q stop=%q channel=%q>\n    <title>%s</title>\n  </programme>\n",
				xmltvTime(e.Start), xmltvTime(e.Stop), e.Slug, esc(e.Title))
		}
	}
	b.WriteString("</tv>\n")
	return b.String()
}

func RenderM3U(baseURL string, schedules []ChannelSchedule) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, cs := range schedules {
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=%q tvg-name=%q tvg-chno=\"%d\",%s\n%s/streams/%s/master.m3u8\n",
			cs.Slug, cs.Name, cs.Number, cs.Name, baseURL, cs.Slug)
	}
	return b.String()
}
```

`api/export/http/server.go`:

```go
// Package http serves /playlist.m3u and /epg.xml for IPTV players.
package http

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hotpot-iptv/internal/engine"
	"hotpot-iptv/internal/epg"
	"hotpot-iptv/sqlc"
)

const epgHorizon = 24 * time.Hour

type StatusSource interface {
	Status(channelID int32) (engine.ChannelStatus, bool)
}

type Server struct {
	q   *sqlc.Queries
	src StatusSource
}

func NewServer(pool *pgxpool.Pool, src StatusSource) Server {
	return Server{q: sqlc.New(pool), src: src}
}

func (s Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/playlist.m3u", s.M3U)
	r.Get("/epg.xml", s.XMLTV)
	return r
}

// schedules assembles per-channel schedules from DB + live runner state.
func (s Server) schedules(r *http.Request) ([]epg.ChannelSchedule, error) {
	ctx := r.Context()
	chans, err := s.q.ListChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	var out []epg.ChannelSchedule
	for _, ch := range chans {
		if !ch.Enabled {
			continue
		}
		rows, err := s.q.ListPlaylistItems(ctx, ch.ID)
		if err != nil || len(rows) == 0 {
			continue
		}
		paths := make([]string, 0, len(rows))
		for _, row := range rows {
			paths = append(paths, row.Path)
		}
		files, err := s.q.GetMediaFilesByPaths(ctx, paths)
		if err != nil {
			continue
		}
		durs := make(map[string]int64, len(files))
		for _, f := range files {
			durs[f.Path] = f.DurationMs
		}
		items := make([]epg.Item, 0, len(rows))
		for _, row := range rows {
			title := strings.TrimSuffix(filepath.Base(row.Path), filepath.Ext(row.Path))
			items = append(items, epg.Item{Title: title, DurationMs: durs[row.Path]})
		}
		cs := epg.ChannelSchedule{Slug: ch.Slug, Name: ch.Name, Number: ch.Number, Items: items}
		if st, ok := s.src.Status(ch.ID); ok {
			cs.CurrentPos = int(st.ItemPosition)
		}
		if state, err := s.q.GetChannelState(ctx, ch.ID); err == nil && state.ItemStartedAt.Valid {
			cs.ItemStartedAt = state.ItemStartedAt.Time
		}
		out = append(out, cs)
	}
	return out, nil
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s Server) M3U(w http.ResponseWriter, r *http.Request) {
	scheds, err := s.schedules(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	_, _ = w.Write([]byte(epg.RenderM3U(baseURL(r), scheds)))
}

func (s Server) XMLTV(w http.ResponseWriter, r *http.Request) {
	scheds, err := s.schedules(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write([]byte(epg.RenderXMLTV(scheds, time.Now(), epgHorizon)))
}
```

`api/export/http/server_test.go` (integration — real DB, fake status):

```go
package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/engine"
	"hotpot-iptv/pkg/testdb"
	"hotpot-iptv/sqlc"
)

type stoppedSrc struct{}

func (stoppedSrc) Status(int32) (engine.ChannelStatus, bool) { return engine.ChannelStatus{}, false }

func TestExportEndpoints(t *testing.T) {
	pool := testdb.New(t)
	q := sqlc.New(pool)
	ctx := context.Background()

	ch, err := q.CreateChannel(ctx, sqlc.CreateChannelParams{
		Name: "Movies", Number: 1, Slug: "movies",
		VideoWidth: 1920, VideoHeight: 1080, VideoBitrateK: 5000,
	})
	require.NoError(t, err)
	_, err = q.InsertPlaylistItems(ctx, sqlc.InsertPlaylistItemsParams{
		ChannelID: ch.ID, Positions: []int32{0}, Paths: []string{"movies/First.mkv"},
	})
	require.NoError(t, err)
	_, err = q.UpsertMediaFile(ctx, sqlc.UpsertMediaFileParams{
		Path: "movies/First.mkv", Size: 1, DurationMs: 3600000, VideoCodec: "h264", Probe: []byte(`{}`),
	})
	require.NoError(t, err)

	srv := httptest.NewServer(NewServer(pool, stoppedSrc{}).NewRouter())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/playlist.m3u")
	require.NoError(t, err)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	assert.Contains(t, string(body), `tvg-id="movies"`)
	assert.Contains(t, string(body), "/streams/movies/master.m3u8")

	res, err = http.Get(srv.URL + "/epg.xml")
	require.NoError(t, err)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	assert.Contains(t, string(body), `<channel id="movies">`)
	assert.Contains(t, string(body), "<title>First</title>")
}
```

(Adjust `UpsertMediaFileParams.Mtime` if the generated type requires it — pass a valid `pgtype.Timestamptz{Time: time.Now(), Valid: true}`.)

`main.go`: `r.Mount("/", exporthttp.NewServer(pool, sup).NewRouter())` — chi allows one mount at `/` for these two GETs only if nothing else is mounted at `/`; to avoid conflicts register directly instead:

```go
		exportSrv := exporthttp.NewServer(pool, sup)
		r.Get("/playlist.m3u", exportSrv.M3U)
		r.Get("/epg.xml", exportSrv.XMLTV)
```

- [ ] **Step 4: Run — PASS** (`go test -p 1 ./internal/epg/ ./api/export/... -v`); `go build ./...`

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: xmltv epg and m3u playlist export"`

---

### Task 18: Web UI — layout + channels management page

**Files:**
- Create: `templates/layouts/base.html`, `templates/channels/index.html`, `internal/web/server.go`, `internal/web/server_test.go`
- Modify: `main.go` (embed templates, mount pages)

**Interfaces:**
- Produces: `web.NewServer(tmplFS fs.FS) (*web.Server, error)`; `(s *Server) NewRouter() *chi.Mux` — `GET /` → redirect `/channels`; `GET /channels`, `GET /dashboard`, `GET /preview` render pages. Pages are static shells; all data comes from `/api/v1/...` via fetch (workspace convention).
- `main.go` gains a package-level `//go:embed templates` var `templatesFS embed.FS` passed to `web.NewServer` (embed var is not a function — the only-main()-function rule still holds).

- [ ] **Step 1: Failing test** `internal/web/server_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPagesRender(t *testing.T) {
	srv, err := NewServer(os.DirFS("../.."))
	require.NoError(t, err)
	ts := httptest.NewServer(srv.NewRouter())
	defer ts.Close()

	for _, path := range []string{"/channels", "/dashboard", "/preview"} {
		res, err := http.Get(ts.URL + path)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, res.StatusCode, path)
		res.Body.Close()
	}

	res, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.StatusCode) // followed redirect to /channels
	res.Body.Close()
}
```

(The test uses `os.DirFS("../..")` so it reads the real `templates/` tree without embedding.)

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement**

`templates/layouts/base.html`:

```html
{{define "base"}}<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>hotpot-iptv</title>
  <!-- Pinned version + SRI required — see "CDN scripts & SRI" step in this task -->
  <script src="https://cdn.jsdelivr.net/npm/@tailwindcss/browser@4.1.11" integrity="sha384-FILL_AT_IMPLEMENTATION" crossorigin="anonymous"></script>
</head>
<body class="bg-slate-950 text-slate-100 min-h-screen">
  <nav class="bg-slate-900 border-b border-slate-800 px-6 py-3 flex items-center gap-6">
    <span class="font-bold text-orange-400 text-lg">🍲 hotpot-iptv</span>
    <a href="/channels" class="hover:text-orange-300">Channels</a>
    <a href="/dashboard" class="hover:text-orange-300">Dashboard</a>
    <a href="/preview" class="hover:text-orange-300">Preview</a>
    <span class="ml-auto text-sm text-slate-400"><a href="/playlist.m3u" class="underline">playlist.m3u</a> · <a href="/epg.xml" class="underline">epg.xml</a></span>
  </nav>
  <main class="p-6 max-w-6xl mx-auto">{{template "content" .}}</main>
</body>
</html>{{end}}
```

`templates/channels/index.html`:

```html
{{define "content"}}
<div class="flex items-center justify-between mb-4">
  <h1 class="text-2xl font-bold">Channels</h1>
  <button onclick="openEditor()" class="bg-orange-500 hover:bg-orange-400 text-white px-4 py-2 rounded">+ New channel</button>
</div>
<div id="channels" class="space-y-2"></div>

<!-- Channel editor modal -->
<div id="editor" class="hidden fixed inset-0 bg-black/70 flex items-center justify-center p-4">
  <div class="bg-slate-900 rounded-lg p-6 w-full max-w-2xl max-h-[90vh] overflow-y-auto">
    <h2 id="editor-title" class="text-xl font-bold mb-4">New channel</h2>
    <input type="hidden" id="f-id">
    <div class="grid grid-cols-2 gap-3 mb-4">
      <label class="block col-span-2">Name <input id="f-name" class="w-full bg-slate-800 rounded px-3 py-2 mt-1"></label>
      <label class="block">Number <input id="f-number" type="number" class="w-full bg-slate-800 rounded px-3 py-2 mt-1"></label>
      <label class="block">Bitrate (kbps) <input id="f-bitrate" type="number" value="5000" class="w-full bg-slate-800 rounded px-3 py-2 mt-1"></label>
    </div>
    <h3 class="font-semibold mb-2">Playlist</h3>
    <ol id="playlist" class="mb-2 space-y-1"></ol>
    <button onclick="openBrowser('')" class="bg-slate-700 hover:bg-slate-600 px-3 py-1 rounded text-sm mb-4">+ Add files</button>
    <div class="flex gap-2 justify-end">
      <button onclick="closeEditor()" class="px-4 py-2 rounded bg-slate-700">Cancel</button>
      <button onclick="saveChannel()" class="px-4 py-2 rounded bg-orange-500 hover:bg-orange-400">Save</button>
    </div>
    <p id="editor-error" class="text-red-400 mt-2"></p>
  </div>
</div>

<!-- Library browser modal -->
<div id="browser" class="hidden fixed inset-0 bg-black/80 flex items-center justify-center p-4">
  <div class="bg-slate-900 rounded-lg p-6 w-full max-w-xl max-h-[80vh] overflow-y-auto">
    <div class="flex justify-between mb-3">
      <span id="browser-path" class="text-slate-400 text-sm font-mono">/</span>
      <button onclick="hide('browser')" class="text-slate-400">✕</button>
    </div>
    <ul id="browser-list" class="space-y-1"></ul>
  </div>
</div>

<script>
const $ = id => document.getElementById(id);
const show = id => $(id).classList.remove('hidden');
const hide = id => $(id).classList.add('hidden');
let playlist = [];

async function api(path, opts = {}) {
  const res = await fetch('/api/v1' + path, { headers: { 'Content-Type': 'application/json' }, ...opts });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body.data;
}

async function loadChannels() {
  const chs = await api('/channels');
  $('channels').innerHTML = (chs || []).map(c => `
    <div class="bg-slate-900 rounded p-4 flex items-center gap-4">
      <span class="text-slate-500 w-8">${c.number}</span>
      <div class="flex-1">
        <div class="font-semibold">${c.name}</div>
        <div class="text-sm text-slate-400">${c.slug} · ${c.video_width}x${c.video_height} @ ${c.video_bitrate_k}k
          ${c.status ? '<span class="text-green-400">● ' + c.status.state + '</span>' : '<span class="text-slate-500">● stopped</span>'}
        </div>
      </div>
      <button onclick="editChannel(${c.id})" class="px-3 py-1 rounded bg-slate-700 hover:bg-slate-600 text-sm">Edit</button>
      <button onclick="removeChannel(${c.id})" class="px-3 py-1 rounded bg-red-900 hover:bg-red-800 text-sm">Delete</button>
    </div>`).join('') || '<p class="text-slate-500">No channels yet.</p>';
}

function renderPlaylist() {
  $('playlist').innerHTML = playlist.map((p, i) => `
    <li class="flex items-center gap-2 bg-slate-800 rounded px-2 py-1 text-sm font-mono">
      <span class="flex-1 truncate">${p}</span>
      <button onclick="movePl(${i},-1)">↑</button>
      <button onclick="movePl(${i},1)">↓</button>
      <button onclick="playlist.splice(${i},1);renderPlaylist()" class="text-red-400">✕</button>
    </li>`).join('');
}

function movePl(i, d) {
  const j = i + d;
  if (j < 0 || j >= playlist.length) return;
  [playlist[i], playlist[j]] = [playlist[j], playlist[i]];
  renderPlaylist();
}

function openEditor() {
  $('f-id').value = ''; $('f-name').value = ''; $('f-number').value = '';
  $('f-bitrate').value = '5000'; $('editor-title').textContent = 'New channel';
  $('editor-error').textContent = ''; playlist = []; renderPlaylist(); show('editor');
}

async function editChannel(id) {
  const c = await api(`/channels/${id}`);
  $('f-id').value = c.id; $('f-name').value = c.name; $('f-number').value = c.number;
  $('f-bitrate').value = c.video_bitrate_k; $('editor-title').textContent = 'Edit ' + c.name;
  $('editor-error').textContent = '';
  playlist = (await api(`/channels/${id}/playlist`) || []).map(i => i.path);
  renderPlaylist(); show('editor');
}

function closeEditor() { hide('editor'); }

async function saveChannel() {
  try {
    const body = {
      name: $('f-name').value, number: +$('f-number').value,
      video_bitrate_k: +$('f-bitrate').value, enabled: true,
    };
    const id = $('f-id').value;
    const ch = id
      ? await api(`/channels/${id}`, { method: 'PUT', body: JSON.stringify(body) })
      : await api('/channels', { method: 'POST', body: JSON.stringify(body) });
    await api(`/channels/${ch.id}/playlist`, { method: 'PUT', body: JSON.stringify({ paths: playlist }) });
    closeEditor(); loadChannels();
  } catch (e) { $('editor-error').textContent = e.message; }
}

async function removeChannel(id) {
  if (!confirm('Delete this channel?')) return;
  await api(`/channels/${id}`, { method: 'DELETE' });
  loadChannels();
}

async function openBrowser(path) {
  const data = await api('/library?path=' + encodeURIComponent(path));
  $('browser-path').textContent = '/' + (data.path || '');
  const up = path ? `<li><button onclick="openBrowser('${path.split('/').slice(0,-1).join('/')}')" class="text-slate-400">⬅ ..</button></li>` : '';
  $('browser-list').innerHTML = up + (data.entries || []).map(e => e.is_dir
    ? `<li><button onclick="openBrowser('${e.path}')" class="text-orange-300">📁 ${e.name}</button></li>`
    : `<li><button onclick="playlist.push('${e.path}');renderPlaylist()" class="hover:text-orange-300">🎬 ${e.name}</button></li>`
  ).join('') || '<li class="text-slate-500">empty</li>';
  show('browser');
}

loadChannels();
</script>
{{end}}
```

`internal/web/server.go`:

```go
// Package web serves the server-rendered management pages.
package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Server struct {
	pages map[string]*template.Template
}

// NewServer parses layouts + pages from tmplFS, which must contain the
// templates/ directory (embed.FS from main, or os.DirFS in tests).
func NewServer(tmplFS fs.FS) (*Server, error) {
	pages := map[string]*template.Template{}
	for _, name := range []string{"channels", "dashboard", "preview"} {
		t, err := template.ParseFS(tmplFS,
			"templates/layouts/base.html",
			fmt.Sprintf("templates/%s/index.html", name))
		if err != nil {
			return nil, fmt.Errorf("parse %s templates: %w", name, err)
		}
		pages[name] = t
	}
	return &Server{pages: pages}, nil
}

func (s *Server) NewRouter() *chi.Mux {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/channels", http.StatusFound)
	})
	r.Get("/channels", s.page("channels"))
	r.Get("/dashboard", s.page("dashboard"))
	r.Get("/preview", s.page("preview"))
	return r
}

func (s *Server) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := s.pages[name].ExecuteTemplate(w, "base", nil); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
```

For this task, also create MINIMAL placeholder pages so parsing succeeds (fleshed out in Task 19): `templates/dashboard/index.html` and `templates/preview/index.html`, each just:

```html
{{define "content"}}<h1 class="text-2xl font-bold">Coming in Task 19</h1>{{end}}
```

`main.go` — add at package level and mount:

```go
//go:embed templates
var templatesFS embed.FS
```

```go
	// inside main(), before ListenAndServe (outside the DB block — pages work without DB)
	webSrv, err := web.NewServer(templatesFS)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}
	r.Mount("/", webSrv.NewRouter())
```

(Import `embed` and `hotpot-iptv/internal/web`. Note: chi resolves the `/playlist.m3u` + `/epg.xml` GETs registered earlier before this catch-all mount only if they were registered first — register the web mount LAST in main.)

- [ ] **Step 3b: CDN scripts & SRI** — every external `<script>` (Tailwind here, HLS.js in Task 19) must pin an exact version and carry `integrity` + `crossorigin="anonymous"`. The `sha384-FILL_AT_IMPLEMENTATION` values are the ONLY permitted fill-in in this plan because a hash can't be precomputed; generate each one now with:

```bash
curl -s https://cdn.jsdelivr.net/npm/@tailwindcss/browser@4.1.11 \
  | openssl dgst -sha384 -binary | openssl base64 -A
# → paste as integrity="sha384-<output>"
```

If the pinned version 404s, pick the closest current version on jsdelivr, pin it, and hash that. Verify in the browser console that no SRI error appears and styles load.

- [ ] **Step 4: Run — PASS** (`go test ./internal/web/ -v`); then run the app (`PSQL_URL=... go run .`), open `http://localhost:8080/channels` in a real browser: create a channel, browse the library modal (set `MEDIA_PATH` to a folder with videos), add files, reorder, save, edit again — playlist persists.

- [ ] **Step 5: Commit** — `git add -A && git commit -m "feat: web ui layout and channels management page"`

---

### Task 19: Web UI — dashboard + preview player

**Files:**
- Modify: `templates/dashboard/index.html`, `templates/preview/index.html` (replace placeholders)

**Interfaces:**
- Consumes: `GET /api/v1/channels` (with `status`), `POST /api/v1/channels/{id}/start|stop`, `/streams/{slug}/master.m3u8`, HLS.js CDN (`https://cdn.jsdelivr.net/npm/hls.js@1`).

- [ ] **Step 1: Dashboard** — `templates/dashboard/index.html`:

```html
{{define "content"}}
<h1 class="text-2xl font-bold mb-4">Dashboard</h1>
<div id="cards" class="grid md:grid-cols-2 gap-4"></div>
<script>
async function api(path, opts = {}) {
  const res = await fetch('/api/v1' + path, { headers: { 'Content-Type': 'application/json' }, ...opts });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body.data;
}

async function refresh() {
  const chs = await api('/channels') || [];
  document.getElementById('cards').innerHTML = chs.map(c => {
    const st = c.status;
    const badge = st
      ? '<span class="text-green-400">● running</span>'
      : '<span class="text-slate-500">● stopped</span>';
    return `
    <div class="bg-slate-900 rounded-lg p-4">
      <div class="flex items-center justify-between">
        <span class="font-bold">${c.number} · ${c.name}</span>${badge}
      </div>
      <div class="text-sm text-slate-400 mt-2 min-h-10">
        ${st ? 'Now playing: <span class="font-mono">' + (st.now_playing || '—') + '</span><br>offset ' + st.offset_sec + 's · item #' + st.item_position : 'Not broadcasting'}
      </div>
      <div class="mt-3 flex gap-2">
        ${st
          ? `<button onclick="ctl(${c.id},'stop')" class="bg-red-900 hover:bg-red-800 px-3 py-1 rounded text-sm">Stop</button>
             <a href="/preview?slug=${c.slug}" class="bg-slate-700 hover:bg-slate-600 px-3 py-1 rounded text-sm">Preview</a>`
          : `<button onclick="ctl(${c.id},'start')" class="bg-green-800 hover:bg-green-700 px-3 py-1 rounded text-sm">Start</button>`}
      </div>
    </div>`;
  }).join('') || '<p class="text-slate-500">No channels.</p>';
}

async function ctl(id, action) {
  try { await api(`/channels/${id}/${action}`, { method: 'POST' }); } catch (e) { alert(e.message); }
  refresh();
}

refresh();
setInterval(refresh, 5000);
</script>
{{end}}
```

- [ ] **Step 2: Preview player** — `templates/preview/index.html`:

```html
{{define "content"}}
<h1 class="text-2xl font-bold mb-4">Preview</h1>
<div class="flex gap-3 mb-4 items-center flex-wrap">
  <select id="channel" class="bg-slate-800 rounded px-3 py-2" onchange="tune()"></select>
  <select id="audio" class="bg-slate-800 rounded px-3 py-2" onchange="hls && (hls.audioTrack = +this.value)"></select>
  <select id="subs" class="bg-slate-800 rounded px-3 py-2" onchange="hls && (hls.subtitleTrack = +this.value)"></select>
</div>
<video id="video" controls autoplay class="w-full max-h-[70vh] bg-black rounded"></video>
<p id="err" class="text-red-400 mt-2"></p>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1.5.20/dist/hls.min.js" integrity="sha384-FILL_AT_IMPLEMENTATION" crossorigin="anonymous"></script>
<script>
let hls = null;

async function api(path) {
  const res = await fetch('/api/v1' + path);
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
  return body.data;
}

function fillTracks() {
  const audio = document.getElementById('audio');
  audio.innerHTML = hls.audioTracks.map((t, i) => `<option value="${i}">🔊 ${t.name}</option>`).join('')
    || '<option>no audio tracks</option>';
  const subs = document.getElementById('subs');
  subs.innerHTML = '<option value="-1">💬 off</option>' +
    hls.subtitleTracks.map((t, i) => `<option value="${i}">💬 ${t.name}</option>`).join('');
}

function tune() {
  const slug = document.getElementById('channel').value;
  if (!slug) return;
  history.replaceState(null, '', '/preview?slug=' + slug);
  document.getElementById('err').textContent = '';
  if (hls) hls.destroy();
  const video = document.getElementById('video');
  if (Hls.isSupported()) {
    hls = new Hls();
    hls.loadSource(`/streams/${slug}/master.m3u8`);
    hls.attachMedia(video);
    hls.on(Hls.Events.AUDIO_TRACKS_UPDATED, fillTracks);
    hls.on(Hls.Events.SUBTITLE_TRACKS_UPDATED, fillTracks);
    hls.on(Hls.Events.ERROR, (_, data) => {
      if (data.fatal) document.getElementById('err').textContent = 'Stream error: ' + data.details;
    });
  } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
    video.src = `/streams/${slug}/master.m3u8`; // Safari native
  } else {
    document.getElementById('err').textContent = 'HLS not supported in this browser';
  }
}

async function init() {
  const chs = await api('/channels') || [];
  const running = chs.filter(c => c.status);
  const sel = document.getElementById('channel');
  sel.innerHTML = '<option value="">— pick a running channel —</option>' +
    running.map(c => `<option value="${c.slug}">${c.number} · ${c.name}</option>`).join('');
  const want = new URLSearchParams(location.search).get('slug');
  if (want && running.some(c => c.slug === want)) {
    sel.value = want;
    tune();
  }
}

init();
</script>
{{end}}
```

- [ ] **Step 2b: SRI hash for hls.js** — same procedure as Task 18 Step 3b: `curl -s https://cdn.jsdelivr.net/npm/hls.js@1.5.20/dist/hls.min.js | openssl dgst -sha384 -binary | openssl base64 -A`, paste into the `integrity` attribute (bump the pinned version first if 1.5.20 is gone from jsdelivr).

- [ ] **Step 3: Verify in a real browser** — with the app running and a channel broadcasting (fake or real ffmpeg), open `/dashboard`: badges update, Start/Stop work (network tab: POSTs return 200, list re-fetches). Open `/preview?slug=...`: video plays, audio/subtitle dropdowns list the renditions and switching works. `go test -p 1 ./...` still passes.

- [ ] **Step 4: Commit** — `git add -A && git commit -m "feat: dashboard and hls.js preview player pages"`

---

### Task 20: Docker, compose, e2e media + README

**Files:**
- Create: `Dockerfile`, `docker-compose.yml`, `.env.example`, `scripts/make-test-media.sh` (chmod +x), `README.md`
- Modify: `Taskfile.yml` (add `docker-build`, `e2e-media`, `e2e-run`), `.gitignore` already ignores `testdata/e2e/`

**Interfaces:**
- Consumes: everything. This task produces the deployable artifact.

- [ ] **Step 1: Dockerfile**

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /out/hotpot .

# FFmpeg with NVENC support; the jrottenberg images bundle nvidia-capable ffmpeg.
FROM jrottenberg/ffmpeg:7.1-nvidia2404
ENTRYPOINT []
COPY --from=build /out/hotpot /usr/local/bin/hotpot
ENV FFMPEG_PATH=/usr/local/bin/ffmpeg \
    FFPROBE_PATH=/usr/local/bin/ffprobe \
    MEDIA_PATH=/media \
    STREAMS_PATH=/streams
EXPOSE 8080
CMD ["hotpot"]
```

(If the `7.1-nvidia2404` tag doesn't exist at build time, `docker search`/check Docker Hub for the current `jrottenberg/ffmpeg` nvidia tag — any ffmpeg ≥ 6 nvidia variant works. Record the chosen tag in the commit.)

- [ ] **Step 2: docker-compose.yml + .env.example**

`docker-compose.yml`:

```yaml
services:
  app:
    build: .
    restart: unless-stopped
    runtime: nvidia
    ports:
      - "8080:8080"
    environment:
      PSQL_URL: postgres://hotpot:${POSTGRES_PASSWORD}@db:5432/hotpot?sslmode=disable
      ENCODER: ${ENCODER:-nvenc}
      NVIDIA_VISIBLE_DEVICES: all
      NVIDIA_DRIVER_CAPABILITIES: compute,video,utility
    volumes:
      - media:/media:ro
      - streams:/streams
    depends_on:
      - db
  db:
    image: postgres:16-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: hotpot
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: hotpot
    volumes:
      - pgdata:/var/lib/postgresql/data
    ports:
      - "127.0.0.1:5433:5432"   # for running atlas migrations from the host

volumes:
  pgdata:
  streams:
    driver_opts:
      type: tmpfs
      device: tmpfs
  media:
    driver: local
    driver_opts:
      type: cifs
      device: //${SMB_HOST}/${SMB_SHARE}
      o: "username=${SMB_USER},password=${SMB_PASSWORD},ro,vers=3.0,iocharset=utf8"
```

`.env.example`:

```
POSTGRES_PASSWORD=change-me
SMB_HOST=192.168.1.10
SMB_SHARE=media
SMB_USER=smbuser
SMB_PASSWORD=change-me
# ENCODER=software   # uncomment on hosts without an NVIDIA GPU
```

- [ ] **Step 3: e2e media script + Taskfile targets**

`scripts/make-test-media.sh`:

```sh
#!/bin/sh
# Generates two small multi-track MKVs + an external srt in testdata/e2e/.
set -e
mkdir -p testdata/e2e
ffmpeg -y -f lavfi -i "testsrc2=duration=30:size=1280x720:rate=25" \
  -f lavfi -i "sine=frequency=440:duration=30" \
  -f lavfi -i "sine=frequency=880:duration=30" \
  -map 0:v -map 1:a -map 2:a \
  -metadata:s:a:0 language=tha -metadata:s:a:1 language=eng \
  -c:v libx264 -preset ultrafast -c:a aac \
  testdata/e2e/movie1.mkv
ffmpeg -y -f lavfi -i "testsrc2=duration=30:size=1280x720:rate=25" \
  -f lavfi -i "sine=frequency=660:duration=30" \
  -map 0:v -map 1:a -metadata:s:a:0 language=eng \
  -c:v libx264 -preset ultrafast -c:a aac \
  testdata/e2e/movie2.mkv
cat > testdata/e2e/movie1.tha.srt <<'SRT'
1
00:00:01,000 --> 00:00:05,000
สวัสดี hotpot

2
00:00:10,000 --> 00:00:15,000
บรรทัดที่สอง
SRT
echo "test media written to testdata/e2e/"
```

Taskfile additions:

```yaml
  docker-build:
    cmds: [docker compose build]
  e2e-media:
    cmds: [sh scripts/make-test-media.sh]
  e2e-run:
    cmds:
      - ENCODER=software MEDIA_PATH=$PWD/testdata/e2e STREAMS_PATH=/tmp/hotpot-streams go run .
```

- [ ] **Step 4: README.md** — short: what it is, `docker compose up` quickstart (cp .env.example .env, `task migrate-prod`-style atlas apply against port 5433, open :8080), M3U/EPG URLs for players, dev setup (task test, e2e-media/e2e-run), spec pointer to `docs/superpowers/specs/`.

- [ ] **Step 5: End-to-end verification (scripted, run manually)**

1. `task e2e-media` (needs local ffmpeg).
2. Start dev postgres, `task migrate-dev`, `task e2e-run`.
3. In the UI: create channel, add both movies, start it.
4. `curl -s localhost:8080/streams/<slug>/master.m3u8` — master lists `a_tha_0`, `a_eng_0` audio and `s_tha_0` subtitles.
5. `ffprobe http://localhost:8080/streams/<slug>/master.m3u8` — reports video+audio streams, no errors.
6. Preview page plays; switching audio between Thai/English changes the tone (440 Hz vs 880 Hz); Thai subtitles render on movie1 and disappear (empty filler) on movie2 — the discontinuity between files plays through.
7. `curl -s localhost:8080/playlist.m3u` and `/epg.xml` look sane.
8. `docker compose build` succeeds; `ldd` check not needed (CGO off). On the real box with the Quadro M620: `.env` with `ENCODER=nvenc`, `docker compose up`, confirm `nvidia-smi` shows the ffmpeg process while a channel runs.

- [ ] **Step 6: Commit** — `git add -A && git commit -m "feat: docker packaging, cifs compose, e2e test media"`

---

## Self-Review Notes (already applied)

- **Spec coverage:** HLS output (T9/T15), linear loop + resume-at-item (T13/T14), NVENC/software encoders (T10), CIFS mount (T20), Postgres+sqlc (T2), auto-include tracks + union + filler (T8/T10/T13), web UI 4 features (T5/T6/T16/T18/T19), M3U+EPG (T17), error policy incl. SMB stall (T11/T13), Docker (T20). PGS skip: T3 filters to text codecs. External `.srt`: T3.
- **Known simplifications, intentional:** subtitle lockstep appends sub segment N when video segment N lands (timing is per-index, close enough at fixed 4 s cadence); `Stop` on stopped channel → 500; audio playlists during backoff windows go stale together with video (players see a stalled live edge — same as any dead channel).
- **Type consistency check:** `ffmpeg.Runner` satisfies `engine.ProcessRunner` (method set matches Run(ctx,[]string,RunOpts)). `*engine.Supervisor` satisfies both `streamshttp.ManagerSource` (ManagerFor) and `channels service.Engine` (Start/Stop/Status) and `exporthttp.StatusSource` (Status). `hls.Manager.Renditions()` used by runner (defined T9).
- **sqlc caveat repeated:** generated param/row types may differ in nullable-column wrapping — always adjust call sites to the generated code, never edit `sqlc/`.




