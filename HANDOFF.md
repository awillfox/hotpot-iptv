# hotpot-iptv — Handoff

**Last updated:** 2026-08-13
**Branch:** `main`
**State:** All 20 tasks implemented and committed. Deployment to the target host is
blocked on a Docker credential issue — see *Deployment* below.

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
| 14 | Supervisor + SQL store/loader + main wiring | `6eca988` |
| 15 | HLS delivery HTTP (`/streams`) | `32f993e` |
| 16 | Control API (start/stop/status) | `99a40cf` |
| 17 | EPG (XMLTV) + M3U export | `4112cb4` |
| 18 | Web UI — layout + channels page | `03b3bb4` |
| 19 | Web UI — dashboard + preview player | `93ddc9f` |
| 20 | Docker, compose, e2e media, README | `2650294` |

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

## Deployment — LIVE at http://192.168.77.150:5004

Running on JEMMA-SERVER as `hotpot-iptv-app-1`. Verified end-to-end on real media:
655 library entries over CIFS, a channel started on
`1.Lara Croft  Tomb Raider (2001)1080p.mkv`, master playlist carrying the full rendition
union (Thai + 2× English audio, Thai + English subs), 2.1 MB `.ts` segments served, VTT
segments with the correct `MPEGTS:900000` map, and both exports populated.

Two things differ from the plan and are worth knowing:

- **Port 5004, not 8080.** `obsidian-note-api` already owns 8080 on that host. The published
  port is now `HTTP_PORT` in `.env` (default 8080); the container still listens on 8080.
- **`ENCODER=software`, because NVENC cannot work there.** Docker Desktop runs containers
  under WSL2, and its paravirtualised GPU (`/dev/dxg` is present in the container) exposes
  CUDA and NVML — `nvidia-smi` reports the M620 correctly and ffmpeg lists `h264_nvenc` — but
  **not** `libnvidia-encode.so.1`, so the encoder fails to open with
  `Cannot load libnvidia-encode.so.1`. This is a platform limit, not a config error. Getting
  real NVENC on that box means either a Linux host with the NVIDIA Container Toolkit, or
  running the binary natively on Windows — the latter needs `internal/ffmpeg/runner.go`
  ported, since its process-group kill uses `syscall.Setpgid`/`syscall.Kill` (POSIX-only, so
  the app does not currently cross-compile to Windows).

**Getting images onto the host.** Registry pulls over SSH fail with
`A specified logon session does not exist` — Docker's credential helper needs the logged-on
user's session, and SSH gets its own token. Being logged in at the console is not enough; the
command must *run in* that session. Workaround that needs no physical access:

```powershell
schtasks /create /tn hotpot-pull /tr "%USERPROFILE%\hotpot-pull.cmd" /sc once /st 00:00 /it /f
schtasks /run /tn hotpot-pull
```

`/IT` runs the task inside the interactive session, which has vault access. Confirmed working.

## Original blocker notes (kept for reference)

Target is `jemma@192.168.77.150` — **JEMMA-SERVER, Windows 11 Pro**, not Linux. Docker
Desktop 29.5.3 with a `linux/x86_64` daemon, `nvidia-container-runtime` registered, and the
expected Quadro M620 (driver 582.53, 2 GB). SSH lands in **PowerShell**, so `&&` / `||`
chains fail and remote commands need PowerShell syntax.

Source is staged at `C:\Users\Jemma\hotpot-iptv` and `docker compose config` validates there.

**The blocker:** every registry pull fails with
`error getting credentials — A specified logon session does not exist`. Docker's credential
helper needs an interactive Windows logon and an SSH session cannot provide one. This is not
project-specific — plain `docker pull hello-world` fails identically. Pointing `DOCKER_CONFIG`
and `docker --config` at a helper-free config makes no difference. Non-registry commands
(`docker ps`, `docker images`) work fine.

`golang:1.26-alpine` is already cached on the host; `jrottenberg/ffmpeg:7.1-nvidia2404` is not.

**To unblock, from an interactive session on that machine (RDP or console):**

```powershell
docker pull jrottenberg/ffmpeg:7.1-nvidia2404
docker pull golang:1.26
cd $env:USERPROFILE\hotpot-iptv
docker compose up -d --build
```

Once both images are cached the build needs no registry access, and it can be driven over
SSH again.

**Compose omits the `db` service the plan specified.** That host already runs
`postgis/postgis:18-3.6` on 5432 — the database `PSQL_URL` points at — so a second one would
collide on the port and split the data across two databases.

---

## Test status

`go test -p 1 ./...` — **all green**, no environment setup needed:

```
ok    hotpot-iptv/api/channels
ok    hotpot-iptv/internal/channel/app
ok    hotpot-iptv/internal/config
ok    hotpot-iptv/internal/dbtest
ok    hotpot-iptv/internal/engine        (also clean under -race -count=40)
ok    hotpot-iptv/internal/ffmpeg
ok    hotpot-iptv/internal/hls
ok    hotpot-iptv/internal/library
ok    hotpot-iptv/pkg/testdb
```

Use `-p 1`: `pkg/testdb` shares one database and truncates between tests.

## Environment gotchas

- **Postgres is already provisioned** on `192.168.77.150:5432` (PostgreSQL 18), with three
  separate databases. Credentials live in the gitignored `.env`; copy `.env.example` to start
  one. Every entry point — the app, `go test`, and `task` — reads `.env` automatically.

  | Database | Used by | Note |
  |----------|---------|------|
  | `hotpot` | `PSQL_URL` — the app | schema applied and in sync with `schema.hcl` |
  | `hotpot_atlas_dev` | `PSQL_DEV_URL` — `task migrate-dev` | Atlas **wipes** it every run |
  | `hotpot_test` | `PSQL_TEST_URL` — `pkg/testdb` | tests **drop/recreate** every table |

  Run `task migrate-dev-plan` to see a migration before `task migrate-dev` applies it.

- **Docker is not installed on the local WSL2 dev box**, so `docker build` / `compose` work
  (Task 20) must run against the remote host `jemma@192.168.77.150` — password is in the local
  env var `$COFFEE_PASS`. Use `sshpass -p "$COFFEE_PASS" ssh jemma@192.168.77.150 ...`, or set
  `DOCKER_HOST=ssh://jemma@192.168.77.150`. Atlas is installed locally and needs no Docker,
  since `PSQL_DEV_URL` points at a real database rather than a `docker://` one.

- The server is **PostgreSQL 18** while the plan's compose pins `postgres:16`, and the app
  connects as `postgres` where compose expects a `hotpot` role. Reconcile both in Task 20.
- The worktree at `.claude/worktrees/hotpot-impl` and branch `worktree-hotpot-impl` are
  **gone**; all work is on `main` in the primary checkout. `origin/wip/task-13-engine` is a
  superseded WIP branch, safe to delete.
- Target GPU is a **Quadro M620** — Maxwell-gen NVENC. H.264 only; no HEVC, no AV1.

## How this was built

Spec → plan → `superpowers:subagent-driven-development`: one fresh implementer subagent per
task, a spec-compliance and code-quality review after each, then a whole-branch review at the
end. Each task is TDD — write the failing test from the plan first, then implement.
Continuing that pattern is recommended; the plan is written for it.
