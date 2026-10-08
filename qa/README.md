# Cairn Phase 19 — End-to-End QA

`qa/e2e.py` is the repeatable acceptance harness for version 1.0. It builds the
server binary, drives real server processes against temporary data directories,
and exercises the Phase 19 validation matrix end to end:

| Scenario | What it verifies |
| --- | --- |
| S01 fresh install | bootstrap flow, session cookie, `/auth/me` |
| S02 existing library | register a pre-populated library, initial scan picks up all files, photos carry `media_type` + `content_hash` |
| S03 new media | file added to the library is picked up by the next scan |
| S04 changed media | appended bytes are reflected in `size_bytes` |
| S05 missing media | deleted file flagged `missing` (via `?status=missing`) |
| S06 moved media | moved file tracked at its new `rel_path` (via `?folder=…`) |
| S07 duplicate content | byte-identical files record equal `content_hash` (the duplicate-detection primitive) |
| S08 search | FTS hits on file name tokens and exact names |
| S09 uploads | multipart upload, metadata, byte-exact download |
| S10 large video | 120 MiB upload + `Range` streaming (`206`, correct `Content-Range`) |
| S11 permissions | grant, admin-only denials, missing-capability `403` |
| S12 sharing | public share, password-protected share (401/200 paths) |
| S13 memories | CRUD, markdown body, versions, refs |
| S14 backups | run, list, verify, restore to a fresh destination |
| S15 ML disabled | `/ml` reports disabled; passes rejected with `503` |
| S16 external drive | unplug → `refresh` → offline; reconnect → online; rescan to present |
| S16b disaster recovery | fresh backup + restore mirrors the documented layout (`server/cairn.db`, `libraries/<id>/library.db`, media under `files/`) |
| S17 upload cap | server with a small `CAIRN_MAX_UPLOAD_BYTES` rejects oversize with `413` |
| S18 ML enabled | similarity signatures computed, face pass + status + list healthy |
| S19 duplicate grouping | `GET /files/duplicates` returns one group per identical-content set, members carry hash + equal size, `limit` respected |

## Running

```sh
make qa            # assumes a build is present; builds bin/cairn if missing
python3 qa/e2e.py --keep   # keep the temp workspace on success/failure
```

Exit code is non-zero if any check fails; on failure the server log tails are
printed. The harness uses only the Python 3 standard library.

## Platform coverage

Platform builds (AMD64, ARM64, native binary, Docker) run in CI:

- `Makefile release/release-cross` — native and cross-compiled binaries +
  SHA256SUMS.
- `.github/workflows/ci.yml` — AMD64 build with a live smoke test of
  `/health`, `/ready`, `/metrics`, and the web title; linux/arm64 artifact;
  `docker build` job.

## Findings during Phase 19

QA caught one release-blocking defect that unit tests missed:

**Index/process-media jobs were enqueued but never executed.** Jobs are stored
per library in SQLite (`index_jobs`), and a `jobs.Worker` existed, but nothing
in production started one — so a fresh server could register libraries but
never actually scan them. Fixed by making `IndexManager` own one background
worker per library (`StartLibraryWorker`), started at boot for registered
libraries (`StartWorkers`) and guaranteed on every `TriggerScan` so new
libraries and reconnecting drives get one too. Workers are anchored to a
server-lifetime context; the metadata processor is wired by `main`.

Regression tests: `TestIndexManagerWorkerExecutesScanJob` and
`TestIndexManagerWorkersRestartIsIdempotent` in
`internal/indexer/indexer_test.go`.

Duplicate detection: since Phase 20, S19 verifies the grouping endpoint
(`GET /files/duplicates`) directly, and the routine never deletes or modifies
any copy automatically (see `docs/media.md` and the roadmap Phase 20 entry).