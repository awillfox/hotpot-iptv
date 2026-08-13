# hotpot-iptv

Turns an SMB media library into linear 24/7 IPTV channels, delivered as
multi-audio / multi-subtitle HLS, managed from a web UI, hardware-encoded with
NVENC.

Point a TV player at `playlist.m3u`, get channels that are always mid-programme
— like broadcast television rather than a video-on-demand list.

## How it works

**Go owns every HLS playlist; FFmpeg never writes one.**

One FFmpeg run per playlist item. FFmpeg writes MPEG-TS segments plus a CSV
segment list; Go tails that CSV and renders the media and master playlists in
memory on request, maintaining a sliding window with discontinuity markers at
item boundaries. A supervisor runs one channel-runner goroutine per channel.
Postgres stores channels, playlists, the probe cache and run state.

Two constants must stay in sync or subtitles drift:

- Every MPEG-TS output uses `-output_ts_offset 10`.
- Every WebVTT segment begins `WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000`
  (900000 ÷ 90 kHz = the same 10 s).

Channels whose files lack a track still expose the full rendition union: a file
with no Thai audio gets a silent Thai track, and no Thai subtitles gets empty
WebVTT segments, so players never see tracks appear and vanish mid-stream.

## Quickstart (Docker)

```sh
cp .env.example .env      # then edit: PSQL_URL, SMB_*, ENCODER
task migrate-dev          # apply schema.hcl to PSQL_URL
docker compose up -d --build
```

Open <http://localhost:8080> — it redirects to the channels page. Create a
channel, add files from the library browser, hit Start.

The compose file assumes an **existing** Postgres reachable at `PSQL_URL`; it
does not run one. `runtime: nvidia` requires the NVIDIA container runtime.
Set `ENCODER=software` on a host with no NVIDIA GPU.

## Folder-backed channels

A channel can be pointed at a folder instead of a hand-picked list. Pick one
with **use as source** in the library browser and the playlist is derived by
walking that folder recursively, then rescanned every few minutes — drop a film
in and it joins the rotation on its own.

Rules worth knowing:

- Files already on the playlist keep their position, files that disappear are
  dropped, and newly found files are appended in random order. Stable positions
  are what keep the EPG accurate for what is already scheduled.
- A refresh is adopted **between items only**, so it never cuts a film short.
- A failed or empty scan is ignored. An unreachable share does not empty a
  channel that is on air.
- **The track list is fixed while a channel runs.** `master.m3u8` cannot gain or
  lose audio/subtitle tracks with players attached, so a newly appeared file
  carrying a language none of the others had will play, but that track is not
  advertised until the channel is restarted.
- Manual reordering is disabled for folder-backed channels, since the next
  rescan would overwrite it.

## Running natively (Windows, for real NVENC)

The binary is pure Go with `CGO_ENABLED=0` and templates embedded, so it is a
single file. On Windows this is the only way to reach NVENC — see Limits.

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags='-s -w' -o hotpot.exe .
```

Put `hotpot.exe` and a `.env` in the same directory (config reads `./.env`
relative to the working directory) and install it as a service:

```powershell
nssm install  hotpot-iptv D:\hotpot-iptv\hotpot.exe
nssm set      hotpot-iptv AppDirectory D:\hotpot-iptv
nssm set      hotpot-iptv AppStdout    D:\hotpot-iptv\service.log
nssm set      hotpot-iptv AppStderr    D:\hotpot-iptv\service.log
nssm set      hotpot-iptv Start        SERVICE_AUTO_START
nssm set      hotpot-iptv ObjectName   .\<user> <password>
nssm start    hotpot-iptv
```

Three things that are easy to get wrong:

- **Use a UNC path for `MEDIA_PATH`, not a mapped drive letter.** Drive letters
  are per-logon-session and invisible to a service; `\\host\share` is not.
- **Run the service as a user that can reach the share.** `LocalSystem` cannot.
- **Add a firewall rule.** Docker publishes ports with its own rules; a native
  process gets none, so it will answer on `127.0.0.1` and nothing else:
  `New-NetFirewallRule -DisplayName "hotpot-iptv" -Direction Inbound -Protocol TCP -LocalPort 8080 -Action Allow`

Confirm the GPU is really being used, rather than trusting that `h264_nvenc`
was accepted:

```powershell
nvidia-smi --query-gpu=utilization.encoder --format=csv
```

## For TV players

| URL | Purpose |
|---|---|
| `http://<host>:8080/playlist.m3u` | Channel list (M3U) |
| `http://<host>:8080/epg.xml` | Programme guide (XMLTV, 24 h horizon) |
| `http://<host>:8080/streams/<slug>/master.m3u8` | One channel's HLS master |

Disabled channels are excluded from both exports. The M3U advertises whatever
host the request arrived on, so it works by IP, hostname or behind a proxy.

## Configuration

Every entry point reads `.env` automatically — the app, `go test`, and `task` —
and a real exported environment variable always overrides the file. See
`.env.example` for the full list and defaults.

The ones you will actually change: `PSQL_URL`, `MEDIA_PATH`, `ENCODER`
(`nvenc` | `software`), and `SMB_*` for the compose CIFS mount.

## Development

```sh
task test        # go test -p 1 ./...   (-p 1: pkg/testdb shares one database)
task run
task e2e-media   # generate multi-track test MKVs into testdata/e2e (needs ffmpeg)
task e2e-run     # run against them with the software encoder
```

`sqlc/` and `schema.sql` are **generated** — never hand-edit. Change
`schema.hcl` and `internal/sql/*.sql`, then `task migrate-dev` and
`task sqlcgen`. `task migrate-dev-plan` shows a migration before applying it.

`PSQL_DEV_URL` and `PSQL_TEST_URL` point at databases that get **wiped**:
Atlas resets its dev database on every run, and `pkg/testdb` drops and
recreates every table. Never aim either at the real one.

## Layout

```
api/          channels · library · streams · export   (HTTP slices)
internal/
  channel/    CQRS core: domain + app/{command,query}
  engine/     supervisor, runner, SQL store + loader
  ffmpeg/     probe, command builder, process runner, segment-list tailer
  hls/        rendition model, playlist manager, VTT parse/split
  epg/        XMLTV + M3U rendering
  web/        page handlers
templates/    layouts/ + one directory per page
```

Design spec and the 20-task implementation plan live in
`docs/superpowers/`. Note that the plan is a design document, not gospel —
several tasks' sample code does not match their own tests. Run the tests.

## Limits

- H.264 only. The target GPU is a Quadro M620 (Maxwell): no HEVC, no AV1, and
  2 GB of VRAM is enough for one or two 1080p sessions, not many.
- **NVENC does not work inside Docker on Windows.** Docker Desktop runs
  containers under WSL2, whose paravirtualised GPU (`/dev/dxg`) exposes CUDA and
  NVML — so `nvidia-smi` works and `h264_nvenc` is listed — but the WSL driver
  ships no `libnvidia-encode.so.1`, so opening the encoder fails. There is
  nothing to mount; the file does not exist on the host side either. Use
  `ENCODER=software` in a container on Windows, or run natively (below).
- Single video rendition per channel — no adaptive bitrate ladder.
- Bitmap subtitles (PGS/VOBSUB) are skipped, not OCR'd or burned in.
- No auth on any endpoint. This is a LAN appliance; do not expose it.
