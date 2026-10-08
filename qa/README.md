# Orchard — End-to-End QA

`qa/e2e.py` is a repeatable acceptance harness for the application shell. It
builds the server binary, drives real server processes against temporary data
directories, and exercises the core validation matrix end to end:

| Scenario | What it verifies |
| --- | --- |
| S01 fresh install | bootstrap flow, session cookie, `/auth/me` |
| S02 existing library | register a pre-populated library, initial scan picks up all files, files carry `media_type` + `content_hash` |
| S03 new file | file added to the library is picked up by the next scan |
| S04 changed file | appended bytes are reflected in `size_bytes` |
| S05 missing file | deleted file flagged `missing` (via `?status=missing`) |
| S06 moved file | moved file tracked at its new `rel_path` (via `?folder=…`) |
| S07 duplicate content | byte-identical files record equal `content_hash` |
| S08 search | FTS hits on file name tokens and exact names |
| S09 uploads | multipart upload, metadata, byte-exact download |
| S10 large upload | 120 MiB upload + `Range` streaming (`206`, correct `Content-Range`) |
| S11 permissions | grant, admin-only denials, missing-capability `403` |
| S12 sharing | public share, password-protected share (401/200 paths) |
| S14 backups | run, list, verify, restore to a fresh destination |
| S16 external drive | unplug → `refresh` → offline; reconnect → online; rescan to present |
| S16b disaster recovery | fresh backup + restore mirrors the documented layout (`server/orchard.db`, `libraries/<id>/library.db`, media under `files/`) |
| S17 upload cap | server with a small `ORCHARD_MAX_UPLOAD_BYTES` rejects oversize with `413` |

## Running

```sh
make qa            # assumes a build is present; builds bin/orchard if missing
python3 qa/e2e.py --keep   # keep the temp workspace on success/failure
```

Exit code is non-zero if any check fails; on failure the server log tails are
printed. The harness uses only the Python 3 standard library.

## Platform coverage

`Makefile release/release-cross` builds native and cross-compiled binaries
(+ SHA256SUMS) for every platform in `PLATFORMS`. There is no CI workflow
checked into this template yet — add one that runs `make test`, `make qa`,
and `make docker-build` when this becomes a real CI target.

## Extending this harness

This matrix covers the generic application shell (auth, libraries, indexing,
files, search, permissions, sharing, backups). When Orchard's library features
(books, documents, quotes, notes, bookmarks, full-text content search) are
implemented, add scenarios for them here rather than starting a second
harness.
