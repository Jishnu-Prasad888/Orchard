# Orchard

> A calm, self-hosted personal library — content first, nothing flashy.

Orchard is a single-binary, self-hosted application for keeping a personal
library of files on storage you control. It never copies, moves, or rewrites
your original files — it indexes them where they already live and treats your
filesystem as the source of truth.

## Current status

**This is the foundation, not the library app yet.** Orchard started as a
restructure of an existing photo/video manager: the photo/video/ML-specific
functionality has been removed, and what remains is a clean, generic
application shell — auth, multi-library management, a file browser, tags,
favorites, notes, trash, sharing, permissions, and backups all work today,
against real indexed files.

The library-specific features this project is ultimately for — books,
documents, custom covers, full-text content search, quotes, notes tied to
passages, bookmarks, and reading progress — are **not implemented yet**. That
is deliberate: this pass only had to produce a solid, honest foundation to
build them on, not a half-built version of them. See [Architecture](#architecture)
for where they are expected to plug in.

## Architecture

- **Go backend**, a single process serving a JSON API and the embedded web
  frontend. SQLite (pure-Go driver, no CGO) with WAL mode and migrations.
- **Two databases**: a server-level database (`internal/db`) for accounts,
  sessions, registered libraries, permissions, shares, and backups; and one
  per-library database (`internal/librarydb`), stored inside a `.orchard/`
  directory at the root of that library, holding the index, folders, tags,
  favorites, and notes for that library's files. A library's own database
  travels with it — unplug the drive, move it elsewhere, reconnect, and
  Orchard recognizes it by its stored identity rather than its current path.
- **Incremental indexer** (`internal/indexer`): scans a library's root,
  tracks new/changed/moved/missing files by size, mtime, and content hash,
  and runs a background job queue per library that survives restarts.
  `indexer.MediaProcessor` is a pluggable extension point for deriving data
  from a file after indexing (e.g. future document metadata extraction or
  cover generation) — nothing is registered there yet.
- **Resource-based authorization** (`internal/authz`, ADR-0005 in spirit):
  capability grants and public shares over arbitrary resource keys, not just
  per-library roles.
- **React + TypeScript frontend**, built with Vite and embedded into the Go
  binary via `go:embed` for single-binary deployment. In development, Vite
  proxies `/api` to the Go backend instead.

### Repository layout

```text
cmd/orchard/           entrypoint
internal/
  audit/               security event (audit) logging
  auth/                users, passwords, sessions, roles
  authz/               resource-based permission grants and shares
  backups/             encrypted/compressed backup, verify, and restore
  config/              environment configuration
  crypto/              shared at-rest encryption kernel (AES-256-GCM)
  db/                  server-level SQLite, pragmas, schema migrations
  favorites/           per-user starred files
  fts/                 safe FTS5 query-expression builder
  httpapi/             HTTP API v1, middleware, and route handlers
  indexer/             filesystem scan, change detection, job workers
  jobs/                persistent per-library job queue
  library/             multi-library registration, identity, reconciliation
  librarydb/           per-library SQLite connection and schema
  likeutil/            SQL LIKE-escaping helper
  logging/             structured logging
  media/               file listing/upload/rename/move/copy/trash repository
  metrics/             dependency-free Prometheus exposition
  notes/               per-file Markdown notes
  sanitize/            path validation (traversal, reserved names)
  search/              FTS5-backed search over indexed files
  tags/                named labels, attach/detach to files
  version/             build metadata
  webui/               embedded single-page app server
web/                   React + TypeScript frontend
qa/                    end-to-end acceptance harness (qa/e2e.py)
scripts/               dev helper scripts
```

### Frontend structure

```text
web/src/
  api/        typed HTTP client and endpoint wrappers (api/client.ts, api/queries.ts)
  auth/       session/auth context, route guard, sign-in splash
  components/
    app-shell/  sidebar, top bar, mobile nav, navigation data, global search
    files/      breadcrumbs, folder grid, file table, filters
    media/      virtualized file grid, selection, favorites, file actions
    ui/         buttons, menus, toasts, the shared icon set
    upload/     drag-and-drop + upload tray
    viewer/     file details/tags/share side panels
  lib/        dates, theme, markdown rendering, focus trap, breakpoints
  pages/      one component per route (see below)
  styles/     design tokens (colors, type, spacing), base styles, shared controls
```

Routes today: `/` (Home), `/files`, `/search`, `/favorites`, `/tags`,
`/trash`, `/shared`, `/libraries`, `/permissions`, `/backups`, `/settings`,
plus the public `/landing`, `/login`, `/setup`, and `/s/:token` (public share
view).

## Development setup

Requirements: Go and Node.

```sh
make web-install    # npm install once
make dev            # run the backend (serves web/dist), wiping local dev data first
make web-dev        # in another terminal: Vite dev server with /api proxy, at :5173
```

The server database and state live in `$XDG_DATA_HOME/orchard`
(`~/.local/share/orchard`). Override with `ORCHARD_DATA_DIR`.

## Running locally

```sh
make build          # builds web/, embeds it, compiles bin/orchard
./bin/orchard        # serves http://127.0.0.1:8090
```

On first run, visit the server and follow the setup flow to create the first
(administrator) account, then add a library — point it at any directory you
want Orchard to index.

## Building

```sh
make build           # production binary with the embedded frontend
make release         # cross-compiled binaries for every platform in PLATFORMS, + SHA256SUMS
make test            # backend (go test -race) + frontend (vitest) tests
make lint            # go vet + golangci-lint (if installed) + eslint
make fmt             # gofmt + prettier
make qa              # end-to-end acceptance harness against a real binary (qa/e2e.py)
```

## Docker

```sh
docker build -t orchard .
docker run -d \
  --name orchard \
  -p 8090:8090 \
  -v orchard-data:/data \
  orchard
```

`scripts/docker-dev.sh` rebuilds the image from the current source tree and
restarts a dev container, wiping local dev data first.

## Configuration

All settings use the `ORCHARD_` prefix.

| Variable                      | Default                             | Description                                                |
| ------------------------------ | ------------------------------------ | ------------------------------------------------------------ |
| `ORCHARD_HTTP_ADDR`            | `127.0.0.1:8090`                    | HTTP listen address                                         |
| `ORCHARD_DATA_DIR`             | `$XDG_DATA_HOME/orchard`            | Server-level data directory                                 |
| `ORCHARD_LOG_LEVEL`            | `info`                              | `debug`, `info`, `warn`, or `error`                         |
| `ORCHARD_WEB_DIST`             | *(embedded frontend)*               | Serve an on-disk build instead (dev)                        |
| `ORCHARD_COOKIE_SECURE`        | `false`                             | Force `Secure` on session cookies (TLS-terminating proxy)    |
| `ORCHARD_MAX_UPLOAD_BYTES`     | `2147483648` (2 GiB)                 | Cap on a single upload body                                  |
| `ORCHARD_SCAN_INTERVAL_SEC`    | `60`                                 | How often every online library is rescanned; `0` disables it |
| `ORCHARD_BACKUP_DIR`           | *(disabled)*                        | Where backups are written; empty disables backups           |
| `ORCHARD_BACKUP_KEEP`          | `4`                                  | Completed backups retained; older are pruned                 |
| `ORCHARD_BACKUP_INTERVAL_MIN`  | `0`                                  | Scheduled backup cadence in minutes (`0` = manual only)      |
| `ORCHARD_BACKUP_PASSPHRASE`    | *(disabled)*                        | Encrypts backups (needed for restore/verify)                  |
| `ORCHARD_ENCRYPTION_PASSPHRASE`| *(disabled)*                        | Encrypts Orchard's on-disk metadata at rest                    |

## Database

Two kinds of SQLite database, both pure-Go (no CGO), WAL mode:

- **Server database** (`$ORCHARD_DATA_DIR/orchard.db`): `server_settings`,
  `users`, `sessions`, `audit_log`, `libraries`, `permission_grants`,
  `shares`, `backups`, `user_settings`. Migrations are embedded SQL files in
  `internal/db/migrations/`, applied in order and tracked in
  `schema_migrations`.
- **Per-library database** (`<library root>/.orchard/library.db`):
  `indexed_files`, `index_jobs`, `folders`, `trash`, `fts_files` (FTS5),
  `tags`, `file_tags`, `favorites`, `file_notes`. The schema is a single
  idempotent DDL block in `internal/librarydb/librarydb.go` (`CREATE TABLE IF
  NOT EXISTS` throughout), applied whenever a library's database is opened —
  because a library can be opened from a different machine than the one that
  created it, this schema can't be a server-side migration.

Nothing here is library-content-specific yet: `indexed_files` already carries
a `media_type` classification (`photo`/`video`/`audio`/`document`/`other`)
that a future book/document importer can use directly, and tags/favorites/
notes already attach to any indexed file, not just a particular type.

## Storage model

**Original files are never modified in place.** A library's root holds your
files exactly as they are; Orchard's own data lives alongside them in a
`.orchard/` directory (the per-library database, and in the future, derived
data). The intended shape for what that directory grows into:

```text
<library root>/.orchard/
  library.db           the per-library database (exists today)
  originals/            — conceptually, your files themselves, in place
  covers/               (future) extracted/generated cover images
  thumbnails/            (future) derived previews
  extracted-text/        (future) OCR/extracted full-text for search
  indexes/               (future) full-text search indexes
```

Only `library.db` exists today. The rest is not implemented — this section
exists so the next person extending Orchard knows where derived data is
expected to live, and that it must never require touching the original file.

## Frontend ↔ backend integration

The frontend is a pure API client (`web/src/api/client.ts`): it never embeds
business logic the backend doesn't already enforce. In development, Vite
(`web/vite.config.ts`) proxies `/api/*` requests to the Go backend at
`127.0.0.1:8090` (override with `ORCHARD_API_TARGET`), so the frontend always
calls same-origin `/api/v1/...` URLs. In production, `make build` runs the
frontend build and copies `web/dist/` into `internal/webui/dist/`, which the
Go binary embeds via `go:embed` and serves at `/`, with client-side routes
falling back to `index.html`.

Session auth is a cookie (`orchard_session`); 401/403 responses raise a
`window` event (`orchard:unauthorized` / `orchard:forbidden`) that the route
guard listens for, rather than every call site handling it individually.

## License

Not yet decided. Choose an open-source license before public release.
