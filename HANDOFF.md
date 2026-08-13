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

## Deployment — LIVE, native Windows service, NVENC working

**`http://192.168.77.150:5004`** — `D:\hotpot-iptv\hotpot.exe`, run by NSSM as service
**`hotpot-iptv`** (auto-start, `ObjectName .\Jemma`, logs to `D:\hotpot-iptv\service.log`).

The Docker deployment is **stopped** (`docker compose down`); its files remain at
`C:\Users\Jemma\hotpot-iptv` if you ever want the container path back.

Why native: **NVENC cannot work in a container on this host.** Docker Desktop runs
containers under WSL2, and the WSL driver directory ships only `libcuda.so.1`,
`libnvidia-ml.so.1` and the PTX JIT compiler — there is no `libnvidia-encode.so.1`
anywhere, so there is nothing to bind-mount. `nvidia-smi` working inside the container is
misleading: that is NVML, not the encoder. Native ffmpeg encodes fine.

Verified on the GPU, not merely accepted by ffmpeg:

```
nvidia-smi --query-compute-apps  -> ...\Gyan.FFmpeg\...\ffmpeg.exe
utilization.gpu 1 %   utilization.encoder 15 %   memory.used 149 MiB
```

Encoder busy with compute near idle is the right signature — the work is on the dedicated
NVENC block, not the shaders.

Native `.env` at `D:\hotpot-iptv\.env`: `PORT=5004`, `ENCODER=nvenc`,
`MEDIA_PATH=\\192.168.77.155\Movies`, `STREAMS_PATH=D:\hotpot-iptv\streams`, same
`PSQL_URL` as before.

Three host-specific traps, all already solved:

- **UNC, not the drive letter.** Session 1 already maps that share as `Y:`, which is why
  `net use` returns error 1219 (already connected under another user). Drive letters are
  per-logon-session and invisible to services; the UNC path is not. Service reads 655
  entries fine.
- **Firewall.** Docker published ports with its own rules; a native process gets none, so
  it answered on `127.0.0.1` and nothing remote. Rule `hotpot-iptv 5004` added.
- **Anything needing the interactive session** — Docker registry pulls, `net use` —
  must run via `schtasks /IT`, because an SSH session gets its own logon token. See below.

The Windows build needed `internal/ffmpeg/runner.go` split into `proc_unix.go` /
`proc_windows.go` (`3b311af`): it used `syscall.Setpgid` and `syscall.Kill(-pid)` and would
not compile for Windows at all. Killing ffmpeg as a *tree* is load-bearing — a surviving
child holds stdout open and hangs the reader — so Windows uses
`CREATE_NEW_PROCESS_GROUP` + `taskkill /T`, and a characterisation test pins the behaviour
on both.

## Previous container deployment (stopped, kept for reference)

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

## Beyond the plan: folder-backed channels

Added after Task 20 (`fc39b63`, `2cdcc6a`). A channel may set `source_folder`; its
playlist is then derived by walking that folder recursively and refreshed on a ticker
(`folderRefreshEvery`, 5 min) instead of being hand-picked. `NULL`/empty keeps the old
behaviour, and the source is a variadic `RunnerOption`, so hand-picked channels are
untouched.

Constraints that are load-bearing — change them and something breaks:

- **Refreshes are adopted only between items** (`adoptPending`), never mid-file.
- **A failed or empty scan is discarded.** An unreachable share must not empty a channel
  that is on air.
- **The rendition union is never recomputed while running.** `master.m3u8` cannot gain or
  lose tracks with players attached, so a new file with an unseen language plays but that
  track is not advertised until restart.
- **The first scan is bounded** to `seedLimit` (10) files. The share holds 1874 videos and
  one ffprobe over SMB takes ~1.5 s, so an unbounded seed inside the start request was
  ~47 min and timed out. The background refresh grows the list to the full folder while
  the channel plays; seeded entries keep their position, the rest append shuffled.
- Probing is resumable: `ensureProbed` upserts per file and skips unchanged size+mtime, so
  an interrupted scan is not wasted.

Live on the deployment: channel 1 `default`, `source_folder = "."` (the whole share).

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

- **Docker is not installed on the local WSL2 dev box.** Anything container-shaped runs on
  `jemma@192.168.77.150` — password is in the local env var `$COFFEE_PASS`, used as
  `sshpass -e ssh jemma@192.168.77.150 ...` (via `SSHPASS`, so it stays out of `ps`). Atlas
  is installed locally and needs no Docker, since `PSQL_DEV_URL` points at a real database
  rather than a `docker://` one.

- **SSH to that host lands in PowerShell**, so `&&` / `||` chains fail and nested quoting
  gets mangled. Write scripts locally and `scp` them rather than fighting the quoting.

- **An SSH session gets its own Windows logon token**, which cannot reach the credential
  vault or that session's drive mappings. Registry pulls and `net use` therefore fail with
  *"A specified logon session does not exist"* or error 1219 — and logging in at the console
  does **not** help, because the command has to *run in* that session. The fix that needs no
  physical access:

  ```powershell
  schtasks /create /tn <name> /tr "%USERPROFILE%\<script>.cmd" /sc once /st 00:00 /it /f
  schtasks /run /tn <name>
  ```

  `/IT` runs it inside the interactive session. Used for the image pulls; confirmed working.

- The server is **PostgreSQL 18** while `docker-compose.yml` pins `postgres:16` in its
  comments, and the app connects as `postgres` rather than a `hotpot` role. Only matters if
  the container path is ever revived — the live deployment is native.
- The worktree at `.claude/worktrees/hotpot-impl` and branch `worktree-hotpot-impl` are
  **gone**; all work is on `main` in the primary checkout. `origin/wip/task-13-engine` is a
  superseded WIP branch, safe to delete.
- Target GPU is a **Quadro M620** — Maxwell-gen NVENC. H.264 only; no HEVC, no AV1, and
  2 GB of VRAM is one or two 1080p sessions, not many. It is in use by the native
  deployment (`utilization.encoder` confirms it) and unreachable from a container on this
  host. Check with `nvidia-smi --query-gpu=utilization.encoder --format=csv` rather than
  trusting that ffmpeg accepted `-c:v h264_nvenc` — it lists the encoder either way.

## How this was built

Spec → plan → `superpowers:subagent-driven-development`: one fresh implementer subagent per
task, a spec-compliance and code-quality review after each, then a whole-branch review at the
end. Each task is TDD — write the failing test from the plan first, then implement.
Continuing that pattern is recommended; the plan is written for it.
