# Cairn

> A small, beautiful, personal digital place that quietly organizes everything you want to keep.

Cairn is a self-hosted personal media, file, and memory server. It brings together
ideas from Google Photos, Google Drive, Obsidian, and personal digital archives into
one calm, self-hosted application.

The most important architectural idea:

> **Cairn catalogs and manages existing media *in place*. It never copies, moves,
> recompresses, or rewrites your original files.**

Your filesystem is the source of truth for actual media files. Point Cairn at a
directory full of years of photos and videos and it indexes them where they are.

## Current status

v1.0 — feature-complete. Phases 0–22 are merged to `main` (see
[docs/roadmap.md](docs/roadmap.md)), including storage libraries, the
incremental indexer, media processing, file browsing, search, memories,
albums/tags, resource-based authorization, sharing, backups, optional local ML,
face recognition, duplicate detection, and the web file-browser + organization
UI.

The foundation is in place:

- Go API server with clean package boundaries
- SQLite with WAL, migrations, and a pure-Go driver (cross-compiles without CGO)
- Embedded React + TypeScript frontend (built into a single binary)
- Health, version, and authentication endpoints with consistent JSON error
  envelopes and request IDs
- User accounts (argon2id password hashing), HTTP-only session cookies,
  Admin/User roles, and audit logging
- Configuration via environment variables
- Build system (`Makefile`), container image, and CI
- Documentation and architecture decision records

See [docs/roadmap.md](docs/roadmap.md) for the full plan.

## Quick start

### From source

```sh
make build          # builds web/, embeds it, compiles bin/cairn
./bin/cairn         # serves http://127.0.0.1:8715
```

The server database and state live in `$XDG_DATA_HOME/cairn`
(`~/.local/share/cairn`). Override with `CAIRN_DATA_DIR`.

### Requirements

- Go and Node (to build from source).
- **ffmpeg** (recommended): Cairn uses it to make video thumbnails. Without it,
  the browser has to decode each video itself, which fails for formats it
  cannot play (for example HEVC/x265 `.mkv`), and those videos show no
  preview. Install it with `sudo apt install ffmpeg` (Debian/Ubuntu),
  `brew install ffmpeg` (macOS) or your platform's package manager, and make
  sure it is on `PATH` for the server. Cairn warns in the app when it is missing.

### Docker

The image already includes ffmpeg.

```sh
docker build -t cairn .
docker run -d \
  --name cairn \
  -p 8715:8715 \
  -v cairn-data:/data \
  cairn
```

### Development

```sh
make web-install    # npm install once
make dev            # run the backend (serves web/dist)
make web-dev        # in another terminal: Vite dev server with /api proxy
```

See [docs/development.md](docs/development.md) for details.

### Video timeline preview

Rest the pointer on a video's timeline for a second and a small frame preview
appears above it. It runs entirely in the browser (a second, muted copy of the
video seeks to the hovered time); the server is not involved. Things you may
notice:

- **No preview for some videos.** It needs a format the browser can decode. If
  the browser cannot show the video's picture (HEVC/x265 on most Linux
  browsers, some exotic `.mkv` codecs), the preview never appears. The video
  may still play with sound only.
- **The time is approximate.** The seek bar is the browser's own control, so
  Cairn estimates where it is from the pointer position. Chrome, Firefox and
  Safari lay it out slightly differently, so the previewed frame can be off by
  a few seconds and the popup may show when you are not exactly on the bar.
- **Fullscreen.** It works in Cairn's fullscreen (the viewer's full-screen
  button or `F`). The browser's own native video fullscreen button covers the
  page, so no preview is shown there.
- **No preview on touch screens.** Phones and tablets have no hover.
- **A short delay.** The preview waits one second, then needs time to seek:
  typically 0.1–0.5 s, but a second or more for videos with long gaps between
  keyframes or whose index sits at the end of the file (some phone and
  screen-recording clips), because the browser must fetch more data first.
- **Extra data.** A second connection to the video is opened the first time you
  hover the timeline, so it reads more of the file while previewing.

## Configuration

All settings use the `CAIRN_` prefix.

| Variable               | Default                            | Description                             |
| ---------------------- | ---------------------------------- | --------------------------------------- |
| `CAIRN_HTTP_ADDR`      | `127.0.0.1:8715`                   | HTTP listen address                     |
| `CAIRN_DATA_DIR`       | `$XDG_DATA_HOME/cairn`             | Server-level data directory             |
| `CAIRN_LOG_LEVEL`      | `info`                             | `debug`, `info`, `warn`, or `error`     |
| `CAIRN_WEB_DIST`       | *(embedded frontend)*              | Serve an on-disk build instead (dev)    |
| `CAIRN_COOKIE_SECURE`  | `false`                            | Force `Secure` on session cookies (TLS-terminating proxy) |
| `CAIRN_BACKUP_DIR`     | *(disabled)*                       | Where backups are written; empty disables backups |
| `CAIRN_BACKUP_KEEP`    | `4`                                | Completed backups retained; older are pruned |
| `CAIRN_BACKUP_INTERVAL_MIN` | `0`                            | Scheduled backup cadence in minutes (`0` = manual only) |
| `CAIRN_BACKUP_PASSPHRASE`| *(disabled)*                     | Encrypts backups (needed for restore/verify) |
| `CAIRN_ENCRYPTION_PASSPHRASE` | *(disabled)*            | Encrypts Cairn's on-disk metadata (.cairn identity + thumbnails) at rest |
| `CAIRN_ML_ENABLED`      | `false`                           | Enable local ML similarity search           |
| `CAIRN_ML_SIMILARITY`   | `true`                            | Run a similarity pass when ML is enabled    |
| `CAIRN_ML_WORKERS`      | `2`                               | Bounded concurrency for the similarity pass |
| `CAIRN_ML_DISTANCE_THRESHOLD` | `10`                        | Hamming distance for flagging near-duplicates |
| `CAIRN_ML_FACE_DETECTOR_MODEL` | *(data dir)/models/face-detector.onnx* | Path to the SCRFD face-detector ONNX model. Download via the ML settings page. |
| `CAIRN_ML_FACE_DETECTOR_URL` | *(InsightFace buffalo_l det_2.5g)* | URL to fetch the SCRFD detector from; override for a mirror or a different model size. |
| `CAIRN_ML_FACE_MODEL`   | *(data dir)/models/face-recognition.onnx* | Path to the ArcFace R50 recognition ONNX model. Download via the ML settings page. |
| `CAIRN_ML_FACE_MODEL_URL` | *(InsightFace buffalo_l w600k_r50)* | URL to fetch the ArcFace recognizer from; override for a mirror. |

## Repository layout

```text
cmd/cairn/             entrypoint
internal/
  audit/               security event (audit) logging
  auth/                users, passwords, sessions, roles
  config/              environment configuration
  db/                  SQLite, pragmas, schema migrations
  httpapi/             HTTP API v1 and request middleware
  logging/             structured logging
  version/             build metadata
  webui/               embedded single-page app server
web/                   React + TypeScript frontend
docs/                  architecture, API, operations documentation
```

## API

The API is REST/JSON under `/api/v1`. It covers health, version, and the
authentication lifecycle (bootstrap, login, logout, current user, and admin
user management). The complete contract is maintained in
[docs/openapi.yaml](docs/openapi.yaml), with prose in [docs/api.md](docs/api.md).
A future client (React Native, desktop, CLI) can be built against the documented
API without reading Go source.

## Documentation

- [Architecture](docs/architecture.md)
- [Development](docs/development.md)
- [API](docs/api.md)
- [Authentication](docs/authentication.md)
- [Security](docs/security.md)
- [Storage model](docs/storage.md)
- [Libraries](docs/libraries.md)
- [Media](docs/media.md)
- [Search](docs/search.md)
- [Memories](docs/memories.md)
- [Permissions](docs/permissions.md)
- [Sharing](docs/sharing.md)
- [Mobile development](docs/mobile-development.md)
- [Database](docs/database.md)
- [Indexing](docs/indexing.md)
- [Installation](docs/installation.md)
- [Deployment](docs/deployment.md)
- [Docker](docs/docker.md)
- [Roadmap](docs/roadmap.md)
- Architecture decision records in [docs/adr](docs/adr)

## License

Not yet decided. Reviewers should choose an open-source license before public
release.