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

## Requirements

- Go 1.24+ to build.
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
Unknown keys are an error. `~` is expanded in `path` and `cache`; relative
paths are relative to the current directory, not to the file.

Logs go to stderr via Go's `log/slog`. Use `-log-level debug` to see every
browse request (with the client's User-Agent and chosen profile), every HTTP
request, and ffmpeg/ffprobe commands with their errors. The DLNA library's own
debug output (mostly SSDP send errors on interfaces without IPv6 multicast
routes) stays hidden unless you also set `-dms-log-level debug`.

`auto` prints short-timestamped, colored lines when stderr is a terminal
(`pretty`; set `NO_COLOR` to disable colors) and logfmt (`text`) otherwise,
e.g. under Docker or systemd, where full timestamps help log collectors.

## Docker

DLNA discovery uses UDP multicast (SSDP), so the container needs the host
network. This works on Linux hosts; Docker Desktop on macOS/Windows does not
pass multicast through.

```bash
docker build -t just-dlna .
```

```bash
docker run -d --name just-dlna --network host -v /path/to/videos:/media:ro -v just-dlna-cache:/cache -e FRIENDLY_NAME="Home DLNA" just-dlna
```

Or edit the volume path in `docker-compose.yml` and run `docker compose up -d`.
To use a config file, mount it at `/etc/just-dlna/just-dlna.yaml` (see the commented line
in `docker-compose.yml`); the image's `MEDIA_PATH`, `CACHE_DIR`, `HTTP_PORT`,
`MEDIA_PORT` and `LOG_LEVEL` environment variables take precedence over it.

## Layout

- `main.go` – flags, logging, wiring, shutdown
- `config.go` – YAML config file loading
- `internal/library` – folder listing, subtitle discovery, ffprobe cache
- `internal/cds` – ContentDirectory Browse hooks for dms
- `internal/media` – video/subtitle HTTP server, ffmpeg subtitle extraction
- `internal/profile` – per-client subtitle handling (LG, generic)
- `internal/didl` – DIDL-Lite XML types
