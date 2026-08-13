# hotpot-iptv — Handoff

**Last updated:** 2026-08-13
**Branch:** `main`
**State:** Tasks 1–13 of 20 complete and committed. **Task 14 is next.**

---

## What this is

A Go service that turns an SMB media library into linear 24/7 IPTV channels, delivered as
multi-audio / multi-subtitle HLS, managed from a web UI, running in Docker with NVENC on a
Quadro M620.

Read these first, in order:

1. `docs/superpowers/specs/2026-08-12-hotpot-iptv-design.md` — the design spec.
2. `docs/superpowers/plans/2026-08-12-hotpot-iptv.md` — the 20-task implementation plan.
   Every task lists exact files, interfaces, and test code. **The plan is the source of truth.**

Note: the plan's checkboxes were never ticked during execution. Ignore them — use the git log
and this document for real progress.

## The one architectural idea that matters

**Go owns all HLS playlists; FFmpeg never writes one.**

One FFmpeg run per playlist item. FFmpeg writes MPEG-TS segments plus a CSV segment list.
Go tails that CSV and renders the media/master playlists in memory on request, maintaining a
sliding window with discontinuity markers at item boundaries. A supervisor runs one
channel-runner goroutine per channel. Postgres stores channels, playlists, probe cache, and
run state.

Two constants that must stay in sync or subtitles drift:

- All MPEG-TS segment outputs use `-output_ts_offset 10`.
- Every WebVTT segment begins `WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000`
  (900000 / 90 kHz = the same 10 s).

## Stack

Go 1.26 · chi + render · pgx/v5 + sqlc + Atlas · viper · testify · testcontainers-go ·
html/template + Tailwind CDN + vanilla JS + HLS.js · FFmpeg (`h264_nvenc`, or `libx264` when
`ENCODER=software`).

## Ground rules

- Module name is `hotpot-iptv`. `CGO_ENABLED=0` for release builds.
- `sqlc/` and `schema.sql` are **generated** — never hand-edit. Edit `schema.hcl` and
  `internal/sql/*.sql`, then regenerate.
- `main.go` contains only `main()` (package-level `//go:embed` vars are allowed).
- All API responses use the envelope `{"data": ..., "error": "..."}` via
  `internal/response.HTTPResponse`.
- No auth on any endpoint — this is a LAN appliance.
- Tests are table-driven. `go test -p 1 ./...` must pass at every commit.
- Conventional commits. `go fmt ./...` before each one.

Defaults: segment 4 s, live window 30 segments, audio AAC 160k stereo, video H.264 only,
1920×1080 @ 5000 kbps.

Env (viper `AutomaticEnv`): `PORT=8080`, `PSQL_URL`, `MEDIA_PATH=/media`,
`STREAMS_PATH=/streams`, `SEGMENT_SECONDS=4`, `WINDOW_SEGMENTS=30`, `ENCODER=nvenc`,
`VIDEO_WIDTH=1920`, `VIDEO_HEIGHT=1080`, `VIDEO_BITRATE_K=5000`, `FFMPEG_PATH=ffmpeg`,
`FFPROBE_PATH=ffprobe`.

---

## Done — Tasks 1–12

| # | Task | Commit |
|---|------|--------|
| 1 | Scaffold — config, health server, Taskfile | `7b9d1f3` |
| 2 | Database — schema.hcl, sqlc, testdb helper | `ff78276` |
| 3 | ffprobe wrapper + external subtitle discovery | `63a46e4` |
| 4 | Channel CQRS core (domain + app, probe-on-add) | `4f93dbd` |
| 5 | api/channels HTTP slice (CRUD + playlist) | `b41614e` |
| 6 | Library browser | `968d3a9` |
| 7 | WebVTT parse + split | `2b5a46e` |
| 8 | HLS rendition model (union + track mapping) | `bf9f0b2` |
| 9 | HLS playlist manager (window, discontinuity) | `b550994`, fix `4af63ca` |
| 10 | FFmpeg command builder | `4361925` |
| 11 | FFmpeg process runner (progress, stall watchdog) | `de99872` |
| 12 | Segment-list CSV tailer | `6de4a46`, fix `c687e28` |
| 13 | Channel runner (engine core) | `6a3f7c8` |

## Careful: the plan is not reliable verbatim

Task 13's plan text is **internally inconsistent** — its Step 3 implementation does not pass
its own Step 1 test. Two independent reasons, both fixed in `6a3f7c8`:

- The plan's fake `ProcessRunner` returns instantly. The runner has no pacing of its own; its
  only throttle is ffmpeg's `-re`. Unthrottled it ran ~2600 items/sec, and the 30-segment
  window evicted the segments the test asserts on **4 ms** after they appeared.
- The plan's test expects `b.mkv` in `000002`, but a failed retry consumes a sequence number,
  so it lands in `000003`. A fresh dir per *attempt* is required — reusing it would make the
  tailer re-read the failed attempt's partial CSV.

Treat later tasks' plan code the same way: useful as a design, not as something to transcribe.
Run the tests.

## Two hardening changes beyond the plan (in `6a3f7c8`)

- `minItemInterval` (1 s) floors the item cycle. Never engages during realtime playback; it
  exists so an input ffmpeg exits on instantly can't hot-spin the loop.
- `sweepStaleItemDirs` deletes item dirs below the oldest still referenced by a live window,
  via the new `hls.Manager.LiveURIs()`. Previously `appendAndClean` removed evicted *segments*
  but never the directory or its CSV/VTT residue — a directory leaked per item played, forever.

**Known, not fixed:** `itemSeq` restarts at 0 on every `NewRunner`, so a restarted channel
rewrites `000001…` over any stale dirs from the previous run. Worth purging the channel's
stream dir on runner start — decide this while wiring the supervisor in Task 14.

## Remaining — Tasks 14–20

| # | Task | Plan line |
|---|------|-----------|
| 14 | Supervisor + SQL store/loader + main wiring | ~3955 |
| 15 | HLS delivery HTTP (`/streams`) | ~4341 |
| 16 | Control API (start/stop/status, enriched list) | ~4541 |
| 17 | EPG (XMLTV) + M3U export | ~4769 |
| 18 | Web UI — layout + channels page | ~5171 |
| 19 | Web UI — dashboard + preview player | ~5485 |
| 20 | Docker, compose, e2e media, README | ~5624 |

---

## Test status

```
ok    hotpot-iptv/internal/config
ok    hotpot-iptv/internal/engine        (also clean under -race -count=40)
ok    hotpot-iptv/internal/ffmpeg
ok    hotpot-iptv/internal/hls
ok    hotpot-iptv/internal/library
FAIL  hotpot-iptv/api/channels           ─┐
FAIL  hotpot-iptv/internal/channel/app    ├ no local Postgres; see gotchas below
FAIL  hotpot-iptv/internal/dbtest        ─┘
```

The three DB-backed failures are environmental, not regressions — they fail identically on a
clean checkout with `dial unix /var/run/postgresql/.s.PGSQL.5432: no such file or directory`.
Run them against the remote Docker host.

## Environment gotchas

- **Docker is not available on the local WSL2 dev box.** DB tests (testcontainers) and any
  `docker build` / `compose` work must run against the remote host
  `jemma@192.168.77.150` — password is in the local env var `$COFFEE_PASS`.
  Use `sshpass -p "$COFFEE_PASS" ssh jemma@192.168.77.150 ...`, or set
  `DOCKER_HOST=ssh://jemma@192.168.77.150`.
- The worktree at `.claude/worktrees/hotpot-impl` and branch `worktree-hotpot-impl` are
  **gone**; all work is on `main` in the primary checkout. `origin/wip/task-13-engine` is a
  superseded WIP branch, safe to delete.
- Target GPU is a **Quadro M620** — Maxwell-gen NVENC. H.264 only; no HEVC, no AV1.

## How this was built

Spec → plan → `superpowers:subagent-driven-development`: one fresh implementer subagent per
task, a spec-compliance and code-quality review after each, then a whole-branch review at the
end. Each task is TDD — write the failing test from the plan first, then implement.
Continuing that pattern is recommended; the plan is written for it.
