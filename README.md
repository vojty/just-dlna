# just-dlna

A small DLNA media server that streams video files from a folder **as they are**:
no transcoding, no library database, no required folder layout or file naming.
Built on [anacrolix/dms](https://github.com/anacrolix/dms) (SSDP discovery,
UPnP ContentDirectory), with subtitle support added on top.

## Features

- Mirrors the folder tree 1:1. Every folder is browsable, every video shows its
  real file name. Folders without videos and hidden files (`.*`) are skipped.
- Videos are streamed byte-for-byte with HTTP range (seek) support.
- Subtitles, always delivered to the TV as SRT:
  - **External files** (`.srt .vtt .ass .ssa .smi`) next to the video whose name
    starts with the video name: `movie.srt`, `movie.en.srt`, `Movie_cz.forced.srt`.
  - If a folder contains **only one video**, every subtitle file in that folder
    and in a `Subs`/`Subtitles` sub-folder is attached, whatever its name.
  - **Embedded** text tracks (MKV/MP4: SRT, ASS, WebVTT, mov_text) are extracted
    with ffmpeg in a single pass and cached. Bitmap tracks (PGS, VobSub) are skipped.
  - Non-UTF-8 subtitle files can be converted with `-sub-charset cp1250`.
- Client profiles: LG webOS (subtitle `<res>` entries) and a generic profile
  (also Samsung-style `CaptionInfo.sec`). See `internal/profile` to add more.
- Web UI (port 1340) to change settings and to upload, rename, move, download
  and delete files in the media folder.

## Requirements

- Go 1.25+ to build, Node.js 24+ to build the web UI.
- `ffmpeg` and `ffprobe` in `PATH` at runtime (media details and subtitle
  conversion). Without them videos and plain UTF-8 `.srt` files still work.

## Run

```bash
go run . -path ~/Videos -name "My DLNA"
```

| Flag | Env | Default | |
|---|---|---|---|
| `-config` | `CONFIG_FILE` | see below | YAML config file |
| `-path` | `MEDIA_PATH` | `.` | media folder |
| `-name` | `FRIENDLY_NAME` | hostname based | name shown on the TV |
| `-http-port` | `HTTP_PORT` | `1338` | DLNA control/description port |
| `-media-port` | `MEDIA_PORT` | `1339` | video/subtitle streaming port |
| `-cache` | `CACHE_DIR` | user cache dir | converted subtitles |
| `-sub-charset` | `SUB_CHARSET` | | charset of non-UTF-8 subtitles, e.g. `cp1250` |
| `-prefetch-subs` | `PREFETCH_SUBS` | `true` | extract embedded subtitles when a video's details are opened |
| `-ifname` | `IFNAME` | all | announce on one network interface |
| `-allowed-ips` | `ALLOWED_IPS` | `0.0.0.0/0,::/0` | allowed client networks |
| `-log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `-dms-log-level` | `DMS_LOG_LEVEL` | `info` | minimum level for the DLNA/SSDP library, on top of `-log-level` |
| `-log-format` | `LOG_FORMAT` | `auto` | `auto`, `pretty`, `text` or `json` |
| `-log-headers` | `LOG_HEADERS` | `false` | dump DLNA HTTP headers (client debugging) |
| `-ui-port` | `UI_PORT` | `1340` | web UI port, `0` disables the web UI |
| `-ui-dir` | `UI_DIR` | `ui/dist` | folder with the built web UI |

### Config file

Settings can also be kept in a YAML file whose keys are the flag names. The
file is loaded automatically from the first of these that exists, or from the
path given with `-config` / `CONFIG_FILE`:

1. `just-dlna.yaml` in the current directory
2. `just-dlna/just-dlna.yaml` in the user config dir (`~/.config` on Linux,
   `~/Library/Application Support` on macOS)
3. `/etc/just-dlna/just-dlna.yaml`

```yaml
path: ~/Videos
name: Home DLNA
sub-charset: cp1250
prefetch-subs: false
allowed-ips: [192.168.1.0/24, fd00::/8]   # or "192.168.1.0/24,fd00::/8"
```

[`just-dlna.example.yaml`](just-dlna.example.yaml) lists every setting with its default.
Command line flags win over environment variables, which win over the file.
Unknown keys are an error. A file given with `-config` that does not exist yet
is created when settings are saved in the web UI; without any config file they
are saved to `just-dlna/just-dlna.yaml` in the user config dir. `~` is expanded in `path` and `cache`; relative
paths are relative to the current directory, not to the file.

Logs go to stderr via Go's `log/slog`. Use `-log-level debug` to see every
browse request (with the client's User-Agent and chosen profile), every HTTP
request, and ffmpeg/ffprobe commands with their errors. The DLNA library's own
debug output (mostly SSDP send errors on interfaces without IPv6 multicast
routes) stays hidden unless you also set `-dms-log-level debug`.

`auto` prints short-timestamped, colored lines when stderr is a terminal
(`pretty`; set `NO_COLOR` to disable colors) and logfmt (`text`) otherwise,
e.g. under Docker or systemd, where full timestamps help log collectors.

## Web UI

The server also serves a small web app on `-ui-port` (default
`http://<host>:1340`):

- **Files**: browse the media folder, upload files (drag and drop, with
  progress and cancel), create folders, rename, move, download and delete.
  Uploads are written to a hidden `.<name>.part` file and renamed when complete.
- **Settings**: edit every setting. Changes are saved to the config file
  (comments in it are kept) and applied with the **Restart now** button, which
  restarts the server process in place. Settings given as command line flags or
  environment variables take precedence over the file, so they are shown
  locked.

There is no authentication yet: anyone who can reach the port can change
settings and delete files. Keep it on a trusted network, or block the port with
a firewall. The media folder must be writable for uploads.

Build the UI once with `npm ci && npm run build` (output in `ui/dist`, served by
the Go server). For UI development run the Go server and `npm run dev`; Vite
serves the app with hot reload and proxies `/api` to `localhost:1340`. Other
scripts: `npm run lint` (Oxlint), `npm run fmt` / `fmt:check` (Oxfmt),
`npm run typecheck`.

The UI is a Vite + React + TypeScript single page app using TanStack Router
(file based routes in `ui/src/routes`), Tailwind CSS and Base UI. Its API is in
`internal/admin`.

## Docker

DLNA discovery uses UDP multicast (SSDP), so the container needs the host
network. This works on Linux hosts; Docker Desktop on macOS/Windows does not
pass multicast through.

```bash
docker build -t just-dlna .
```

```bash
docker run -d --name just-dlna --network host -v /path/to/videos:/media -v just-dlna-cache:/cache -v just-dlna-config:/config just-dlna
```

Or edit the volume path in `docker-compose.yml` and run `docker compose up -d`.
The image builds the web UI and serves it on port 1340. Settings saved there go
to `/config/just-dlna.yaml` (`CONFIG_FILE`), so keep `/config` on a volume. The
image sets `MEDIA_PATH=/media` and `CACHE_DIR=/cache`, so those two are locked
in the web UI; the same goes for any `-e` variable you add. The container runs
just-dlna as `PUID`:`PGID` (default 1000:1000) and hands `/cache` and `/config`
to that user on start. That user needs write access to the media folder for
uploads; mount it with `:ro` if you do not want that.

## Layout

- `main.go` – flags, logging, wiring, shutdown
- `config.go` – YAML config file loading
- `internal/library` – folder listing, subtitle discovery, ffprobe cache
- `internal/cds` – ContentDirectory Browse hooks for dms
- `internal/media` – video/subtitle HTTP server, ffmpeg subtitle extraction
- `internal/profile` – per-client subtitle handling (LG, generic)
- `internal/didl` – DIDL-Lite XML types
- `internal/admin` – web UI server and its JSON API (settings, file management)
- `settings.go` – settings shown in the web UI, saving the config file
- `ui/` – web UI sources (tooling config and `package.json` in the repo root)
