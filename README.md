# just-dlna

A small DLNA server that streams videos from a folder **as they are**. No
transcoding, no library database, no rules for folder or file names. Built on
[anacrolix/dms](https://github.com/anacrolix/dms), with subtitle support added.

> **Security is not a goal right now.** just-dlna is meant for a trusted home
> network: there is no login, and anyone who can reach it can stream, upload,
> rename and delete files in the media folder. Do not expose it to the
> internet.

## Features

- Shows your folders exactly as they are on disk. Folders with no videos and
  hidden files (`.*`) are skipped.
- Streams videos unchanged, with seeking.
- Sends subtitles to the TV as SRT:
  - **Files next to the video** (`.srt .vtt .ass .ssa .smi`) whose name starts
    with the video name, e.g. `movie.srt`, `movie.en.srt`.
  - If a folder has **only one video**, all subtitle files in that folder and
    in a `Sub`/`Subs`/`Subtitles` sub-folder are used.
  - **Embedded** text subtitles (MKV/MP4) are extracted with ffmpeg and cached.
    Image subtitles (PGS, VobSub) are not supported.
  - Non-UTF-8 files can be converted, e.g. `-sub-charset cp1250`.
- Client profiles for LG webOS and a generic one (also for Samsung). Add more in
  `internal/profile`.
- Web UI (port 1340) to change settings and manage files.

## Requirements

- Go 1.27+ and Node.js 24+ to build.
- `ffmpeg` and `ffprobe` in `PATH`. Without them, videos and UTF-8 `.srt` files
  still work.

## Run

```bash
go run . -path ~/Videos -name "My DLNA"
```

| Flag | Env | Default | |
|---|---|---|---|
| `-config` | `CONFIG_FILE` | see below | YAML config file |
| `-path` | `MEDIA_PATH` | `.` | media folder |
| `-name` | `FRIENDLY_NAME` | from hostname | name shown on the TV |
| `-http-port` | `HTTP_PORT` | `1338` | DLNA control port |
| `-media-port` | `MEDIA_PORT` | `1339` | streaming port |
| `-cache` | `CACHE_DIR` | user cache dir | converted subtitles |
| `-sub-charset` | `SUB_CHARSET` | | charset of non-UTF-8 subtitles |
| `-prefetch-subs` | `PREFETCH_SUBS` | `true` | extract embedded subtitles when a video's details are opened |
| `-ifname` | `IFNAME` | LAN interfaces | comma-separated interfaces to announce on, e.g. `wlo1,enp1s0` |
| `-log-level` | `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `-dms-log-level` | `DMS_LOG_LEVEL` | `info` | log level of the DLNA library |
| `-log-format` | `LOG_FORMAT` | `auto` | `auto`, `pretty`, `text`, `json` |
| `-log-headers` | `LOG_HEADERS` | `false` | log DLNA HTTP headers |
| `-ui-port` | `UI_PORT` | `1340` | web UI port, `0` turns it off |
| `-ui-dir` | `UI_DIR` | `ui/dist` | built web UI folder |

### Config file

You can put settings in a YAML file. Keys are the flag names. The first file
found is used:

1. `-config` / `CONFIG_FILE`
2. `just-dlna.yaml` in the current directory
3. `just-dlna/just-dlna.yaml` in the user config dir (`~/.config` on Linux,
   `~/Library/Application Support` on macOS)
4. `/etc/just-dlna/just-dlna.yaml`

```yaml
path: ~/Videos
name: Home DLNA
sub-charset: cp1250
```

See [`just-dlna.example.yaml`](just-dlna.example.yaml) for all settings.

- Flags win over env variables, env variables win over the file.
- Unknown keys are an error.
- `~` works in `path` and `cache`. Relative paths are relative to the current
  directory.
- With no config file, the web UI saves to the user config dir.

### Logs

Logs go to stderr. Use `-log-level debug` to see requests, the detected client
profile and ffmpeg commands. Add `-dms-log-level debug` for the DLNA library's
own logs (mostly noise). `auto` format is colored on a terminal (turn off with
`NO_COLOR`) and plain logfmt otherwise.

## Web UI

Open `http://<host>:1340`.

- **Files**: browse, upload (drag and drop), create folders, rename, move,
  download and delete.
- **Settings**: edit settings, save them to the config file, then click
  **Restart now**. Settings set by flags or env variables are locked. The
  media, cache and config folders can only be set by flags, env variables or
  the config file, so the web UI can never reach files outside the media folder.

**There is no login.** Anyone who can reach the port can upload, rename and
delete files in the media folder. Use it only on a trusted network.

To keep other web sites from using it through your browser, the web UI
rejects changes sent from other sites.

## Docker

DLNA discovery uses multicast, so the container needs `--network host`. This
works on Linux only, not on Docker Desktop for macOS/Windows.

```bash
docker run -d --name just-dlna --network host -v /path/to/videos:/media -v just-dlna-cache:/cache -v just-dlna-config:/config ghcr.io/vojty/just-dlna
```

Or edit the media path in `docker-compose.yml` and run `docker compose up -d`.
For CasaOS, import `casaos-compose.yml`.

- Settings from the web UI are saved to `/config`, so keep it on a volume.
- The app runs as `PUID`:`PGID` (default 1000:1000). This user needs write
  access to the media folder for uploads. Mount it with `:ro` for read-only.

### Host network caveats

With the host network, just-dlna sees every interface of the host. Without
`-ifname` / `IFNAME` it announces only on interfaces that look like a LAN: up,
multicast-capable, Ethernet or Wi-Fi, with an IPv4 or global/ULA IPv6 address.
It skips loopback, point-to-point links and these:

- **Thread** (`wpan*`, 802.15.4 link types): an OpenThread Border Router (Home
  Assistant, Matter) creates `wpan0`. SSDP announcements flood the slow mesh
  and can crash Thread devices.
- **Docker and VMs** (`docker*`, `br-*`, `veth*`, `virbr*`): no TVs there.
- **VPNs** (`tun*`, `tap*`, `wg*`, `zt*`, `tailscale*`).

The log lists each interface with why it was chosen or skipped. Still, set
`IFNAME` to your LAN interface (e.g. `eth0` or `wlo1`, see `ip -br addr`) on
such hosts, so a new virtual interface can never be picked up.

## Development

```bash
scripts/run.sh -path ~/Videos
```

This starts the Go server and the Vite dev server with hot reload.

- Build the UI: `npm ci && npm run build` (output in `ui/dist`).
- Checks: `go test ./...`, `npm run typecheck`, `npm run lint`, `npm run knip`,
  `npm run fmt:check`.
- After changing the API, run `scripts/gen-api.sh` and commit `openapi.json`
  and `ui/src/client`. CI fails if they are out of date. API docs are at
  `/api/docs`.

### Image size

Most of the Docker image is Alpine's `ffmpeg` package, which pulls in every
video codec and hardware library (x264, x265, libvpx, aom, dav1d, Vulkan,
VA-API, ...). `apk add --no-cache` already leaves no cache behind. just-dlna
only uses ffprobe for media info and ffmpeg for converting subtitles to SRT,
so none of those codecs are needed.

Possible fix: build ffmpeg in a separate stage with `--disable-everything`
and enable only the `file` protocol, all demuxers and parsers (so ffprobe can
still identify streams), the text subtitle decoders/encoders (`subrip`, `ass`,
`ssa`, `webvtt`, `mov_text`, ...) and the `srt` muxer. Then copy just
`ffmpeg` and `ffprobe` into the final image. That would make the layer a few
MB instead of 100+ MB, but the build gets slower (especially for other
architectures) and the configure flags have to be kept up to date.

The UI uses Vite, React, TypeScript, TanStack Router, Tailwind CSS and Base UI.
The API uses Huma.

## Layout

- `main.go` – flags, logging, startup
- `config.go` – config file loading
- `settings.go` – web UI settings, saving the config file
- `internal/library` – folder listing, subtitle discovery, ffprobe cache
- `internal/cds` – DLNA browse handling
- `internal/media` – streaming server, subtitle extraction
- `internal/netif` – choosing the network interfaces to announce on
- `internal/profile` – per-client subtitle handling
- `internal/didl` – DIDL-Lite XML types
- `internal/admin` – web UI server and API
- `cmd/openapi` – prints the OpenAPI document
- `ui/` – web UI sources
