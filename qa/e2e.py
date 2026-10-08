#!/usr/bin/env python3
"""Cairn v1.0 end-to-end QA harness (Phase 19).

Builds the server binary and drives real server processes against temporary
data directories, exercising the Phase 19 validation matrix:

  S01 fresh install (bootstrap)        S10 large video upload + Range streaming
  S02 existing library (pre-indexed)   S11 permissions (grant, deny paths, 403s)
  S03 new media                        S12 sharing (public + password-protected)
  S04 changed media                    S13 memories (CRUD, versions, refs)
  S05 missing media                    S14 backups (run, list, verify, restore)
  S06 moved media                      S15 ML disabled behavior
  S07 duplicate content (hash)         S16 disconnect/reconnect (external drive)
  S08 search                           S17 upload cap (413)
  S09 uploads                          S18 ML enabled (similarity + faces)
  S19 duplicate grouping API

Platform coverage (AMD64/ARM64/native/Docker) is exercised by CI; see
qa/README.md for how the pieces fit together.

Usage: python3 qa/e2e.py [--binary PATH] [--keep]
Exit code 0 only when every scenario passes.
"""

import base64
import http.cookiejar
import json
import os
import random
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

# ---------------------------------------------------------------- fixtures

import struct
import zlib


def make_png(w, h, rgb):
    """Build a valid PNG image (8-bit RGB) with the standard library."""
    def chunk(typ, data):
        body = struct.pack(">I", len(data)) + typ + data
        return body + struct.pack(">I", zlib.crc32(typ + data) & 0xFFFFFFFF)

    sig = b"\x89PNG\r\n\x1a\n"
    ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
    raw = b"".join(b"\x00" + bytes(rgb) * w for _ in range(h))
    return sig + chunk(b"IHDR", ihdr) + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b"")


def photo_bytes():
    """A small valid photo the metadata pipeline accepts (12x8 forest-ish)."""
    return make_png(12, 8, (90, 140, 90))


def make_library(root, big=False):
    """Populate a library root with fixtures matching a real user's library."""
    os.makedirs(root, exist_ok=True)
    with open(os.path.join(root, "IMG_0001.png"), "wb") as f:
        f.write(photo_bytes())
    with open(os.path.join(root, "notes.txt"), "w") as f:
        f.write("holiday plans and packing list\n")
    with open(os.path.join(root, "clip.mp4"), "wb") as f:
        f.write(os.urandom(3 * 1024 * 1024))  # random bytes, video by extension
    if big:
        with open(os.path.join(root, "big.mp4"), "wb") as f:
            f.write(os.urandom(120 * 1024 * 1024))  # large video (~120 MiB)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def random_hex(n=8):
    return "".join(random.choice("0123456789abcdef") for _ in range(n))


# ---------------------------------------------------------------- results

class Report:
    def __init__(self):
        self.rows = []
        self.failures = []

    def check(self, section, desc, ok, detail=""):
        self.rows.append((section, desc, ok, detail))
        mark = "PASS" if ok else "FAIL"
        print(f"  [{mark}] {section}: {desc}" + (f" — {detail}" if detail else ""))
        if not ok:
            self.failures.append((section, desc, detail))

    def summary(self):
        total = len(self.rows)
        passed = sum(1 for _, _, ok, _ in self.rows if ok)
        print(f"\nQA summary: {passed}/{total} checks passed")
        if self.failures:
            print("Failures:")
            for s, d, det in self.failures:
                print(f"  - {s}: {d} {det}")
            return 1
        return 0


REPORT = Report()


# ---------------------------------------------------------------- client

class Client:
    """Minimal HTTP client with cookie jar and JSON/multipart support."""

    def __init__(self, base):
        self.base = base
        self.jar = http.cookiejar.CookieJar()

    def req(self, method, path, body=None, headers=None, raw=None, ctype=None):
        url = self.base + path
        data = None
        hdrs = dict(headers or {})
        if raw is not None:
            data = raw
            if ctype:
                hdrs["Content-Type"] = ctype
        elif body is not None:
            data = json.dumps(body).encode()
            hdrs["Content-Type"] = "application/json"
        opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        req = urllib.request.Request(url, data=data, headers=hdrs, method=method)
        try:
            with opener.open(req, timeout=120) as resp:
                return resp.status, dict(resp.headers), resp.read()
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers), e.read()

    def json(self, method, path, body=None, headers=None):
        status, hdrs, raw = self.req(method, path, body=body, headers=headers)
        parsed = None
        try:
            parsed = json.loads(raw) if raw else None
        except ValueError:
            pass
        return status, parsed, raw

    def upload(self, lib_id, rel_path, local_path, content_type="image/jpeg"):
        """Multipart form upload: fields file + path."""
        boundary = "----cairnqa" + random_hex(12)
        with open(local_path, "rb") as f:
            file_bytes = f.read()
        parts = []
        parts.append(f"--{boundary}\r\n".encode())
        parts.append(
            (
                f'Content-Disposition: form-data; name="file"; filename="{os.path.basename(local_path)}"\r\n'
                f"Content-Type: {content_type}\r\n\r\n"
            ).encode()
        )
        parts.append(file_bytes)
        parts.append(f"\r\n--{boundary}\r\n".encode())
        parts.append(
            ('Content-Disposition: form-data; name="path"\r\n\r\n' + rel_path + "\r\n").encode()
        )
        parts.append(f"--{boundary}--\r\n".encode())
        body = b"".join(parts)
        return self.req(
            "POST",
            f"/api/v1/libraries/{lib_id}/files/upload",
            raw=body,
            ctype=f"multipart/form-data; boundary={boundary}",
        )


# ---------------------------------------------------------------- server

class Server:
    def __init__(self, name, work, env=None, port=None):
        self.name = name
        self.port = port or free_port()
        self.data = os.path.join(work, f"data-{name}")
        self.log = os.path.join(work, f"server-{name}.log")
        self.proc = None
        os.makedirs(self.data, exist_ok=True)
        base_env = dict(os.environ)
        base_env.update(
            {
                "CAIRN_DATA_DIR": self.data,
                "CAIRN_HTTP_ADDR": f"127.0.0.1:{self.port}",
                "CAIRN_LOG_LEVEL": "info",
            }
        )
        if env:
            base_env.update(env)
        self.env = base_env

    @property
    def base(self):
        return f"http://127.0.0.1:{self.port}"

    def start(self, binary):
        self.logf = open(self.log, "w")
        self.proc = subprocess.Popen(
            [binary], env=self.env, stdout=self.logf, stderr=subprocess.STDOUT
        )
        deadline = time.time() + 30
        while time.time() < deadline:
            if self.proc.poll() is not None:
                self.logf.close()
                raise RuntimeError(f"{self.name} exited early; log:\n{open(self.log).read()[-2000:]}")
            try:
                with urllib.request.urlopen(
                    f"{self.base}/api/v1/ready", timeout=1
                ) as resp:
                    if resp.status == 200:
                        return
            except Exception:
                time.sleep(0.25)
        self.stop()
        raise RuntimeError(f"{self.name} not ready in 30s; log:\n{open(self.log).read()[-2000:]}")

    def stop(self):
        if self.proc:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                self.proc.kill()
            self.logf.close()
            self.proc = None


def poll(fn, want, timeout, desc, section):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        last = fn()
        try:
            if want(last):
                return last
        except Exception:
            pass
        time.sleep(0.5)
    REPORT.check(section, desc, False, f"timeout; last={last!r}")
    return last


def wait_index_done(client, lib_id, section, timeout=60):
    def st():
        status, body, _ = client.json("GET", f"/api/v1/libraries/{lib_id}/index/status")
        if status != 200:
            return {"error": status}
        return (body or {}).get("status") or {}
    def done(b):
        # IndexStatus is wrapped under "status". The scan is fully drained only
        # when no job is running AND nothing is queued: active_job and
        # last_job_id are both absent. a queued-but-not-yet-claimed job would
        # otherwise let the wait complete prematurely.
        return ("present" in b and "missing" in b
                and not b.get("active_job")
                and not b.get("last_job_id"))
    got = poll(st, done, timeout, "index scan completes", section)
    if isinstance(got, dict) and "present" in got:
        return got
    return {}


# ---------------------------------------------------------------- run

def main():
    import argparse

    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="bin/cairn")
    ap.add_argument("--keep", action="store_true", help="keep temp dirs on failure")
    args = ap.parse_args()

    binary = os.path.abspath(args.binary)
    if not os.path.exists(binary):
        print("building binary...")
        subprocess.run(
            ["go", "build", "-o", binary, "./cmd/cairn"], check=True, cwd=repo_root()
        )

    work = tempfile.mkdtemp(prefix="cairn-qa-", dir="/tmp/opencode")
    libs = os.path.join(work, "libraries")
    os.makedirs(libs)
    servers = []

    def start_server(name, env=None):
        base = {"CAIRN_BACKUP_DIR": os.path.join(work, "backups")}
        if env:
            base.update(env)
        s = Server(name, work, env=base)
        s.start(binary)
        servers.append(s)
        return s

    try:
        main_srv = start_server("main")
        C = Client(main_srv.base)

        # ---- S01 fresh install / bootstrap -------------------------------
        s, body, _ = C.json("GET", "/api/v1/auth/status")
        REPORT.check("S01", "auth/status reports bootstrap required", s == 200 and body.get("bootstrap_required") is True, f"{s} {body}")
        s, body, _ = C.json(
            "POST", "/api/v1/auth/bootstrap",
            {"username": "admin", "password": "admin-pass-1234"},
        )
        REPORT.check("S01", "bootstrap creates the admin account", s == 201 and (body or {}).get("user", {}).get("username") == "admin", f"{s}")
        s, body, _ = C.json("GET", "/api/v1/auth/me")
        REPORT.check("S01", "bootstrap cookie authenticates /auth/me", s == 200 and (body or {}).get("user", {}).get("username") == "admin", f"{s}")

        # ---- S02 existing library ----------------------------------------
        lib_main = os.path.join(libs, "photos-main")
        make_library(lib_main)
        s, body, _ = C.json("POST", "/api/v1/libraries", {"path": lib_main, "name": "Photos"})
        REPORT.check("S02", "register pre-populated library", s == 201 and (body or {}).get("library", {}).get("id"), f"{s} {body}")
        lib_id = (body or {}).get("library", {}).get("id")

        s, body, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/index", {})
        REPORT.check("S02", "index trigger returns 202", s == 202, f"{s}")
        st = wait_index_done(C, lib_id, "S02")
        REPORT.check("S02", "initial scan indexes 3 pre-existing files", st.get("present") == 3, f"present={st.get('present')}")

        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files")
        files = (body or {}).get("files", [])
        by_name = {f["name"]: f for f in files}
        REPORT.check(
            "S02", "files listed with expected names and present status",
            {f["name"] for f in files} == {"IMG_0001.png", "notes.txt", "clip.mp4"}
            and all(f["status"] == "present" for f in files),
            f"{[f['name'] for f in files]}",
        )
        img = by_name.get("IMG_0001.png", {})
        REPORT.check("S02", "photos carry media_type + content hash", img.get("media_type") == "photo" and img.get("content_hash"), f"{img.get('media_type')} hash={bool(img.get('content_hash'))}")

        # ---- S03 new media ------------------------------------------------
        with open(os.path.join(lib_main, "IMG_0002.png"), "wb") as f:
            f.write(photo_bytes())
        C.json("POST", f"/api/v1/libraries/{lib_id}/index", {})
        st = wait_index_done(C, lib_id, "S03")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files")
        names = {f["name"] for f in (body or {}).get("files", [])}
        REPORT.check("S03", "new media picked up by reindex", "IMG_0002.png" in names and st.get("present") == 4, f"present={st.get('present')} names={names}")

        # ---- S04 changed media --------------------------------------------
        notes = os.path.join(lib_main, "notes.txt")
        before = os.path.getsize(notes)
        with open(notes, "ab") as f:
            f.write(b"x" * 2048)
        C.json("POST", f"/api/v1/libraries/{lib_id}/index", {})
        wait_index_done(C, lib_id, "S04")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files")
        size_after = next((f["size_bytes"] for f in (body or {}).get("files", []) if f["name"] == "notes.txt"), None)
        REPORT.check("S04", "changed file size reflected", size_after is not None and size_after > before, f"before={before} after={size_after}")

        # ---- S05 missing media --------------------------------------------
        os.unlink(os.path.join(lib_main, "clip.mp4"))
        C.json("POST", f"/api/v1/libraries/{lib_id}/index", {})
        st = wait_index_done(C, lib_id, "S05")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files?status=missing")
        clip = next((f for f in (body or {}).get("files", []) if f["name"] == "clip.mp4"), None)
        REPORT.check("S05", "removed file flagged missing", st.get("missing") == 1 and (clip or {}).get("status") == "missing", f"missing={st.get('missing')} status={(clip or {}).get('status')}")

        # ---- S06 moved media ----------------------------------------------
        os.makedirs(os.path.join(lib_main, "muenchen"), exist_ok=True)
        shutil.move(os.path.join(lib_main, "IMG_0001.png"), os.path.join(lib_main, "muenchen", "IMG_0001.png"))
        C.json("POST", f"/api/v1/libraries/{lib_id}/index", {})
        wait_index_done(C, lib_id, "S06")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files?folder=muenchen")
        moved = next((f for f in (body or {}).get("files", []) if f["name"] == "IMG_0001.png"), None)
        REPORT.check("S06", "moved file tracked at new path", (moved or {}).get("rel_path") == "muenchen/IMG_0001.png" and (moved or {}).get("status") == "present", f"rel_path={(moved or {}).get('rel_path')}")

        # ---- S07 duplicate content ----------------------------------------
        shutil.copyfile(os.path.join(lib_main, "IMG_0002.png"), os.path.join(lib_main, "IMG_0002_copy.png"))
        C.json("POST", f"/api/v1/libraries/{lib_id}/index", {})
        wait_index_done(C, lib_id, "S07")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files")
        dup = {f["name"]: f.get("content_hash") for f in (body or {}).get("files", []) if f["name"].startswith("IMG_0002")}
        hashes = set(dup.values())
        REPORT.check(
            "S07", "identical bytes recorded with equal content hash",
            "IMG_0002.png" in dup and "IMG_0002_copy.png" in dup and len(hashes) == 1 and None not in hashes,
            f"{dup}",
        )

        # ---- S19 duplicate grouping API -----------------------------------
        # The group endpoint must flag same-content files (including the copy
        # created in S07 and IMG_0001.png, which shares its bytes).
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files/duplicates")
        groups = (body or {}).get("groups", [])
        total = (body or {}).get("total", -1)
        h = next(iter(hashes), None)
        hit = next((g for g in groups if g.get("content_hash") == h), None)
        members = sorted(m.get("rel_path", "") for m in (hit or {}).get("files", []))
        sizes = {m.get("size_bytes") for m in (hit or {}).get("files", [])}
        REPORT.check(
            "S19", "duplicate groups API flags same-content files",
            s == 200 and total == 1 and hit is not None
            and any("IMG_0002.png" in p for p in members)
            and any("IMG_0002_copy.png" in p for p in members),
            f"total={total} group_hash={bool(hit)} members={members}",
        )
        REPORT.check(
            "S19", "duplicate group members carry hash + equal size",
            hit is not None and len(sizes) == 1 and None not in sizes and len(members) == 3,
            f"sizes={sizes} members={members}",
        )
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files/duplicates?limit=1")
        REPORT.check(
            "S19", "duplicate groups respond to limit param",
            s == 200 and len((body or {}).get("groups", [])) <= 1,
            f"groups={len((body or {}).get('groups', []))}",
        )

        # ---- S08 search ----------------------------------------------------
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/search?q=0002&limit=10")
        hit = [f["name"] for f in (body or {}).get("files", [])]
        REPORT.check("S08", "search by file name token returns match", s == 200 and any("IMG_0002" in n for n in hit), f"q=0002 -> {hit}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/search?q=notes.txt&limit=10")
        REPORT.check("S08", "search by exact file name", s == 200 and any(f["name"] == "notes.txt" for f in (body or {}).get("files", [])), f"q=notes.txt -> {[f['name'] for f in (body or {}).get('files', [])]}")

        # ---- S09 uploads ---------------------------------------------------
        up = os.path.join(work, "upload.png")
        img_bytes = photo_bytes()
        with open(up, "wb") as f:
            f.write(img_bytes)
        s, hdrs, raw = C.upload(lib_id, "uploads/IMG_0003.png", up, content_type="image/png")
        uploaded = None
        try:
            uploaded = json.loads(raw).get("file")
        except Exception:
            pass
        REPORT.check("S09", "multipart upload returns 201 with file", s == 201 and uploaded and uploaded.get("rel_path") == "uploads/IMG_0003.png", f"{s}")
        fid = (uploaded or {}).get("id")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/files/{fid}")
        REPORT.check("S09", "uploaded file metadata readable", s == 200 and (body or {}).get("file", {}).get("status") == "present", f"{s}")
        s, hdrs, raw = C.req("GET", f"/api/v1/libraries/{lib_id}/files/{fid}/download")
        REPORT.check("S09", "download returns the original bytes", s == 200 and raw == img_bytes, f"{s} len={len(raw)}")

        # ---- S10 large video + Range --------------------------------------
        big = os.path.join(work, "big.mp4")
        with open(big, "wb") as f:
            f.write(os.urandom(120 * 1024 * 1024))
        s, hdrs, raw = C.upload(lib_id, "videos/big.mp4", big, content_type="video/mp4")
        bigf = None
        try:
            bigf = json.loads(raw).get("file")
        except Exception:
            pass
        REPORT.check("S10", "120 MiB video uploads", s == 201 and bigf and bigf.get("size_bytes") == 120 * 1024 * 1024, f"{s}")
        s, hdrs, raw = C.req("GET", f"/api/v1/libraries/{lib_id}/files/{bigf['id']}/download", headers={"Range": "bytes=0-1023"})
        REPORT.check(
            "S10", "Range request streams 206 with correct slice",
            s == 206 and len(raw) == 1024 and hdrs.get("Content-Range", "").startswith(f"bytes 0-1023/{120*1024*1024}"),
            f"{s} len={len(raw)} content-range={hdrs.get('Content-Range')}",
        )

        # ---- S11 permissions ----------------------------------------------
        s, body, _ = C.json("POST", "/api/v1/users", {"username": "bob", "password": "bob-pass-1234", "role": "user"})
        bob_id = (body or {}).get("user", {}).get("id")
        REPORT.check("S11", "admin creates limited user", s == 201 and bob_id, f"{s}")
        s, body, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/permissions", {"user_id": bob_id, "key": lib_id, "caps": ["read"], "effect": "allow"})
        REPORT.check("S11", "read grant created", s == 201, f"{s} {body}")
        B = Client(main_srv.base)
        s, body, _ = B.json("POST", "/api/v1/auth/login", {"username": "bob", "password": "bob-pass-1234"})
        REPORT.check("S11", "bob logs in", s == 200, f"{s}")
        s, body, _ = B.json("GET", f"/api/v1/libraries/{lib_id}/files")
        REPORT.check("S11", "bob reads library (grant)", s == 200 and isinstance((body or {}).get("files"), list), f"{s}")
        s, _, _ = B.json("GET", "/api/v1/users")
        REPORT.check("S11", "bob cannot list users (admin-only)", s == 403, f"{s}")
        s, _, _ = B.json("POST", "/api/v1/libraries", {"path": lib_main})
        REPORT.check("S11", "bob cannot register libraries (admin-only)", s == 403, f"{s}")
        s, _, _ = B.req("GET", f"/api/v1/libraries/{lib_id}/files/{fid}/download")
        REPORT.check("S11", "bob lacks download capability -> 403", s == 403, f"{s}")

        # ---- S12 sharing ---------------------------------------------------
        s, body, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/shares", {"key": lib_id, "caps": ["read", "download"]})
        token = (body or {}).get("token")
        REPORT.check("S12", "share created, raw token returned once", s == 201 and token, f"{s}")
        A = Client(main_srv.base)  # anonymous jar
        s, body, _ = A.json("GET", f"/api/v1/shares/{token}")
        REPORT.check("S12", "public share info without session", s == 200, f"{s}")
        s, body, _ = A.json("GET", f"/api/v1/shares/{token}/files?limit=5")
        REPORT.check("S12", "public share file list without session", s == 200 and len((body or {}).get("files", [])) > 0, f"{s} files={len((body or {}).get('files', []))}")
        s, body, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/shares", {"key": lib_id, "caps": ["read"], "password": "share-secret-9"})
        token2 = (body or {}).get("token")
        REPORT.check("S12", "password-protected share created", s == 201 and token2, f"{s}")
        s, _, _ = A.json("GET", f"/api/v1/shares/{token2}/files")
        REPORT.check("S12", "protected share denies without password", s == 401, f"{s}")
        s, _, _ = A.json("GET", f"/api/v1/shares/{token2}/files", headers={"X-Cairn-Share-Password": "wrong-password"})
        REPORT.check("S12", "protected share denies wrong password", s == 401, f"{s}")
        s, body, _ = A.json("GET", f"/api/v1/shares/{token2}/files", headers={"X-Cairn-Share-Password": "share-secret-9"})
        REPORT.check("S12", "protected share grants with correct password", s == 200 and isinstance((body or {}).get("files"), list), f"{s}")

        # ---- S13 memories --------------------------------------------------
        s, body, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/memories", {"title": "Honeymoon", "body": "# Honeymoon\n\nWe visited **Munich** and the Alps."})
        mem_id = (body or {}).get("memory", {}).get("id")
        REPORT.check("S13", "memory created with markdown", s == 201 and mem_id, f"{s}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/memories")
        REPORT.check("S13", "memory listed", s == 200 and any(m.get("id") == mem_id for m in (body or {}).get("memories", [])), f"{s}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/memories/{mem_id}")
        mem = (body or {}).get("memory", {})
        REPORT.check("S13", "memory readable with markdown body", s == 200 and mem.get("title") == "Honeymoon" and "# Honeymoon" in mem.get("body", ""), f"{s}")
        s, body, _ = C.json("PUT", f"/api/v1/libraries/{lib_id}/memories/{mem_id}", {"title": "Honeymoon", "body": "# Honeymoon\n\nUpdated."})
        REPORT.check("S13", "memory updated", s == 200 and "Updated." in (body or {}).get("memory", {}).get("body", ""), f"{s}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/memories/{mem_id}/versions")
        REPORT.check("S13", "memory versions tracked", s == 200 and len((body or {}).get("versions", [])) >= 2, f"{s}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/memories/{mem_id}/refs")
        REPORT.check("S13", "memory refs endpoint works", s == 200, f"{s}")

        # ---- S14 backups ---------------------------------------------------
        s, body, _ = C.json("POST", "/api/v1/backups")
        backup_id = (body or {}).get("id")
        REPORT.check("S14", "backup runs", s == 200 and backup_id and (body or {}).get("status") == "completed", f"{s} {str(body)[:120]}")
        s, body, _ = C.json("GET", "/api/v1/backups")
        backups = body if isinstance(body, list) else []
        all_ids = [b.get("id") for b in backups]
        REPORT.check("S14", "backup listed", s == 200 and backup_id in all_ids, f"{s} ids={all_ids}")
        s, body, _ = C.json("POST", f"/api/v1/backups/{backup_id}/verify")
        REPORT.check("S14", "backup verify", s == 200, f"{s} {str(body)[:120]}")
        dest = os.path.join(work, "restore-dest")
        s, body, _ = C.json("POST", f"/api/v1/backups/{backup_id}/restore", {"destination": dest})
        restored = os.path.isdir(dest) and len(os.listdir(dest)) > 0
        REPORT.check("S14", "backup restore to fresh destination", s == 200 and restored, f"{s} dest_exists={os.path.isdir(dest)}")

        # ---- S15 ML disabled ----------------------------------------------
        s, body, _ = C.json("GET", f"/api/v1/libraries/{lib_id}/ml")
        REPORT.check("S15", "ML status reports disabled", s == 200 and body.get("enabled") is False, f"{s} {body}")
        s, _, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/ml/similarity/pass", {})
        REPORT.check("S15", "similarity pass refused when disabled", s == 503, f"{s}")
        s, _, _ = C.json("POST", f"/api/v1/libraries/{lib_id}/ml/faces/cluster", {})
        REPORT.check("S15", "face cluster refused when disabled", s == 503, f"{s}")

        # ---- S16 disconnect / reconnect (external drive) -------------------
        lib_drive = os.path.join(libs, "drive")
        make_library(lib_drive)
        s, body, _ = C.json("POST", "/api/v1/libraries", {"path": lib_drive, "name": "Drive"})
        drive_id = (body or {}).get("library", {}).get("id")
        C.json("POST", f"/api/v1/libraries/{drive_id}/index", {})
        wait_index_done(C, drive_id, "S16")
        moved_away = lib_drive + "-unplugged"
        os.rename(lib_drive, moved_away)
        s, body, _ = C.json("POST", f"/api/v1/libraries/{drive_id}/refresh", {})
        REPORT.check("S16", "refresh reconciles to online/offline status", s == 200, f"{s} {str(body)[:100]}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{drive_id}")
        REPORT.check("S16", "library reports offline when volume unplugged", s == 200 and (body or {}).get("library", {}).get("status") == "offline", f"{s} status={(body or {}).get('library', {}).get('status')}")
        os.rename(moved_away, lib_drive)
        s, body, _ = C.json("POST", f"/api/v1/libraries/{drive_id}/refresh", {})
        REPORT.check("S16", "refresh after reconnect reconciles to online", s == 200, f"{s}")
        s, body, _ = C.json("GET", f"/api/v1/libraries/{drive_id}")
        REPORT.check("S16", "library reports online after reconnect", s == 200 and (body or {}).get("library", {}).get("status") == "online", f"{s} status={(body or {}).get('library', {}).get('status')}")
        C.json("POST", f"/api/v1/libraries/{drive_id}/index", {})
        st = wait_index_done(C, drive_id, "S16")
        REPORT.check("S16", "reconnected library rescans to present state", st.get("present") == 3 and st.get("missing") == 0, f"present={st.get('present')} missing={st.get('missing')}")

        # ---- S16b disaster recovery: backup the full world, restore, boot ----
        s, body, _ = C.json("POST", "/api/v1/backups")
        backup2 = (body or {}).get("id")
        rec = None
        deadline = time.time() + 60
        while time.time() < deadline:
            _, body, _ = C.json("GET", "/api/v1/backups")
            rec = next((b for b in (body if isinstance(body, list) else []) if b.get("id") == backup2), None)
            if rec and rec.get("status") == "completed":
                break
            time.sleep(0.5)
        REPORT.check("S16b", "backup after reconnect completes", bool(rec) and rec.get("status") == "completed", f"{str(rec)[:120]}")
        dest2 = os.path.join(work, "restore-dest2")
        s, _, _ = C.json("POST", f"/api/v1/backups/{backup2}/restore", {"destination": dest2})
        # Restore mirrors the documented layout: server/cairn.db + libraries/<id>/...
        srv_db = os.path.join(dest2, "server", "cairn.db")
        lib1_db = os.path.join(dest2, "libraries", lib_id, "library.db")
        lib2_db = os.path.join(dest2, "libraries", drive_id, "library.db")
        media_ok = any(os.path.isfile(os.path.join(dest2, "libraries", lib_id, "files", p)) for p in ("notes.txt", "IMG_0001.png"))
        REPORT.check("S16b", "restore mirrors server DB layout", s == 200 and os.path.isfile(srv_db), f"{s} srv_db={os.path.isfile(srv_db)}")
        REPORT.check("S16b", "restore mirrors library DBs for all libraries", os.path.isfile(lib1_db) and os.path.isfile(lib2_db), f"main={os.path.isfile(lib1_db)} drive={os.path.isfile(lib2_db)}")
        REPORT.check("S16b", "restore brings back media files", media_ok, f"media={media_ok}")

        # ---- S17 upload cap (413) -----------------------------------------
        cap_srv = start_server("cap", env={"CAIRN_MAX_UPLOAD_BYTES": "2048"})
        CC = Client(cap_srv.base)
        CC.json("POST", "/api/v1/auth/bootstrap", {"username": "admin", "password": "admin-pass-1234"})
        lib_cap = os.path.join(libs, "cap-lib")
        os.makedirs(lib_cap, exist_ok=True)
        s, body, _ = CC.json("POST", "/api/v1/libraries", {"path": lib_cap})
        cap_id = (body or {}).get("library", {}).get("id")
        bigup = os.path.join(work, "toobig.bin")
        with open(bigup, "wb") as f:
            f.write(os.urandom(4096))
        s, hdrs, raw = CC.upload(cap_id, "toobig.bin", bigup, content_type="application/octet-stream")
        REPORT.check("S17", "oversized upload rejected with 413", s == 413, f"{s}")

        # ---- S18 ML enabled ------------------------------------------------
        ml_srv = start_server("ml", env={"CAIRN_ML_ENABLED": "true", "CAIRN_ML_FACES": "true"})
        M = Client(ml_srv.base)
        M.json("POST", "/api/v1/auth/bootstrap", {"username": "admin", "password": "admin-pass-1234"})
        lib_ml = os.path.join(libs, "photos-ml")
        make_library(lib_ml)
        s, body, _ = M.json("POST", "/api/v1/libraries", {"path": lib_ml})
        ml_id = (body or {}).get("library", {}).get("id")
        M.json("POST", f"/api/v1/libraries/{ml_id}/index", {})
        wait_index_done(M, ml_id, "S18", timeout=120)
        s, body, _ = M.json("GET", f"/api/v1/libraries/{ml_id}/ml")
        REPORT.check("S18", "ML reports enabled", s == 200 and body.get("enabled") is True and body.get("provider"), f"{s} {body}")
        s, _, _ = M.json("POST", f"/api/v1/libraries/{ml_id}/ml/similarity/pass", {})
        REPORT.check("S18", "similarity pass accepted when enabled", s == 202, f"{s}")

        def ml_status():
            _, b, _ = M.json("GET", f"/api/v1/libraries/{ml_id}/ml")
            return b or {}
        got = poll(lambda: ml_status().get("signatured"), lambda v: v and v >= 1, 120, "similarity signatures appear", "S18")
        REPORT.check("S18", "signatures computed for indexed photos", (got or 0) >= 1, f"signatured={got}")
        s, _, _ = M.json("POST", f"/api/v1/libraries/{ml_id}/ml/faces/cluster", {})
        REPORT.check("S18", "face clustering accepted when enabled", s == 202, f"{s}")
        s, body, _ = M.json("GET", f"/api/v1/libraries/{ml_id}/ml/faces")
        REPORT.check("S18", "face status endpoint healthy", s == 200 and isinstance((body or {}).get("faces"), int) and (body or {}).get("enabled") is True, f"{s} {str(body)[:100]}")
        s, body, _ = M.json("GET", f"/api/v1/libraries/{ml_id}/faces")
        REPORT.check("S18", "face list endpoint healthy", s == 200 and isinstance((body or {}).get("faces"), list), f"{s} {str(body)[:80]}")

        # ---- cleanup and report -------------------------------------------
        for srv in servers:
            srv.stop()
        code = REPORT.summary()
        if code != 0:
            for srv in servers:
                try:
                    print(f"\n--- {srv.name} log tail ---\n{open(srv.log).read()[-1500:]}")
                except OSError:
                    pass
        if not args.keep:
            shutil.rmtree(work, ignore_errors=True)
        else:
            print(f"\nQA workdir kept: {work}")
        return code
    finally:
        for srv in servers:
            srv.stop()


def repo_root():
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


if __name__ == "__main__":
    sys.exit(main())