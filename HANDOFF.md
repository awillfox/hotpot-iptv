# hotpot-iptv — Handoff

**Last updated:** 2026-08-13
**Branch:** `worktree-hotpot-impl`
**State:** Tasks 1–12 of 20 complete and committed. Task 13 partially done and **currently failing**.

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

## In progress — Task 13: channel runner (engine core)

`internal/engine/runner.go` and `internal/engine/runner_test.go` exist. **Both tests fail:**

```
--- FAIL: TestRunnerPlaysThroughAndLoops (5.76s)
--- FAIL: TestRunnerSkipsFailingItemAfterRetry (5.74s)
    runner_test.go:171: condition not met in time
```

The failure mode looks like the runner advancing position non-deterministically — the debug
output shows items replaying out of order (`played pos 0` followed by `playing pos 0` again).
There are also leftover `DEBUG:` `fmt.Print` calls in the code and a `TempDir` cleanup error
(the runner leaves files open under `movies/` after the test finishes).

**Start here.** Read Task 13 in the plan (line ~3508), then fix or rewrite. Do not build
Task 14 on top of a red runner.

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
ok    hotpot-iptv/api/channels
ok    hotpot-iptv/internal/channel/app
ok    hotpot-iptv/internal/config
ok    hotpot-iptv/internal/dbtest        (cached — needs Docker to actually run)
FAIL  hotpot-iptv/internal/engine        <-- Task 13
ok    hotpot-iptv/internal/ffmpeg
ok    hotpot-iptv/internal/hls
ok    hotpot-iptv/internal/library
```

## Environment gotchas

- **Docker is not available on the local WSL2 dev box.** DB tests (testcontainers) and any
  `docker build` / `compose` work must run against the remote host
  `jemma@192.168.77.150` — password is in the local env var `$COFFEE_PASS`.
  Use `sshpass -p "$COFFEE_PASS" ssh jemma@192.168.77.150 ...`, or set
  `DOCKER_HOST=ssh://jemma@192.168.77.150`.
- The work lives in a **locked git worktree** at `.claude/worktrees/hotpot-impl`.
  `git worktree list` from the repo root shows it.
- Target GPU is a **Quadro M620** — Maxwell-gen NVENC. H.264 only; no HEVC, no AV1.

## How this was built

Spec → plan → `superpowers:subagent-driven-development`: one fresh implementer subagent per
task, a spec-compliance and code-quality review after each, then a whole-branch review at the
end. Each task is TDD — write the failing test from the plan first, then implement.
Continuing that pattern is recommended; the plan is written for it.
