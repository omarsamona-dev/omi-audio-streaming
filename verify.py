#!/usr/bin/env python3
"""CC-PROMPT-101 verification harness (run on the VPS).

Exercises the omi-audio receiver structurally: valid WAV round-trip with
header parse + ffprobe duration, plus auth negatives that must write NO file,
plus per-uid/per-date folder routing.

Usage: python3 verify.py <container_ip> <token>
"""
import math
import os
import struct
import subprocess
import sys
import urllib.request
import urllib.error

IP = sys.argv[1]
TOKEN = sys.argv[2]
BASE = f"http://{IP}:8080"
DATA = "/opt/omi-audio/data"
SR = 16000
SECS = 10

fails = []


def ok(name):
    print(f"  PASS  {name}")


def bad(name, detail):
    print(f"  FAIL  {name}: {detail}")
    fails.append(name)


def make_pcm(seconds=SECS, sr=SR):
    """5s silence + 5s 440Hz tone, PCM16 mono little-endian."""
    out = []
    for n in range(sr * seconds):
        if n < sr * (seconds // 2):
            v = 0
        else:
            v = int(0.3 * 32767 * math.sin(2 * math.pi * 440 * n / sr))
        out.append(v)
    return struct.pack("<%dh" % len(out), *out)


def post(uid, body, token=TOKEN, sample_rate=SR):
    url = f"{BASE}/audio?token={token}&sample_rate={sample_rate}&uid={uid}"
    req = urllib.request.Request(
        url, data=body, method="POST",
        headers={"Content-Type": "application/octet-stream"},
    )
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, r.read().decode(errors="replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")
    except (urllib.error.URLError, ConnectionError) as e:
        # Connection reset == server rejected mid-stream. Treat as a reject
        # signal (no status). The drain fix should make this rare.
        return None, f"conn-error: {e}"


def list_files(uid):
    root = os.path.join(DATA, uid)
    found = []
    for dp, _dn, fn in os.walk(root):
        for f in fn:
            found.append(os.path.join(dp, f))
    return found


def parse_wav(path, body_len, sr=SR):
    with open(path, "rb") as f:
        h = f.read(44)
        data = f.read()
    checks = {
        "RIFF": h[0:4] == b"RIFF",
        "WAVE": h[8:12] == b"WAVE",
        "fmt ": h[12:16] == b"fmt ",
        "fmt_size16": struct.unpack("<I", h[16:20])[0] == 16,
        "pcm_fmt1": struct.unpack("<H", h[20:22])[0] == 1,
        "mono": struct.unpack("<H", h[22:24])[0] == 1,
        "sr": struct.unpack("<I", h[24:28])[0] == sr,
        "bits16": struct.unpack("<H", h[34:36])[0] == 16,
        "data_tag": h[36:40] == b"data",
        "data_len==body": struct.unpack("<I", h[40:44])[0] == body_len,
        "actual_data==body": len(data) == body_len,
        "riff_size": struct.unpack("<I", h[4:8])[0] == 36 + body_len,
    }
    return checks


def ffprobe_duration(path):
    out = subprocess.check_output(
        ["ffprobe", "-v", "error", "-show_entries", "format=duration",
         "-of", "default=nw=1:nk=1", path], text=True).strip()
    return float(out)


print("== Phase A: allowlist UNSET (prod config) ==")

# 1. healthz
try:
    with urllib.request.urlopen(f"{BASE}/healthz", timeout=10) as r:
        if r.status == 200 and r.read().decode().strip() == "ok":
            ok("healthz 200 ok")
        else:
            bad("healthz", f"status {r.status}")
except Exception as e:
    bad("healthz", str(e))

# 2. valid POST + WAV parse + ffprobe
body = make_pcm()
before = set(list_files("verify_omar"))
st, _ = post("verify_omar", body)
if st != 200:
    bad("valid POST 200", f"got {st}")
else:
    ok("valid POST 200")
    after = set(list_files("verify_omar"))
    new = sorted(after - before)
    if len(new) != 1:
        bad("exactly one file written", f"{len(new)} new files")
    else:
        path = new[0]
        sz = os.path.getsize(path)
        if sz == 44 + len(body):
            ok(f"file size 44+{len(body)} == {sz}")
        else:
            bad("file size", f"{sz} != {44+len(body)}")
        for k, v in parse_wav(path, len(body)).items():
            if v:
                ok(f"wav:{k}")
            else:
                bad(f"wav:{k}", "header mismatch")
        try:
            dur = ffprobe_duration(path)
            if abs(dur - SECS) < 0.05:
                ok(f"ffprobe duration {dur:.3f}s ~= {SECS}s")
            else:
                bad("ffprobe duration", f"{dur} != ~{SECS}")
        except Exception as e:
            bad("ffprobe", str(e))

# 3. wrong token -> 401, no file
before = set(list_files("verify_badtoken"))
st, _ = post("verify_badtoken", body, token="deadbeefwrongtoken")
if st == 401:
    ok("wrong token -> 401")
else:
    bad("wrong token 401", f"got {st}")
after = set(list_files("verify_badtoken"))
if before == after:
    ok("wrong token wrote NO file")
else:
    bad("wrong token no-file", f"new files {after-before}")

# 4. traversal uid -> 400, no escape
st, _ = post("..%2f..%2fevil", body)
if st == 400:
    ok("traversal uid -> 400")
else:
    bad("traversal uid 400", f"got {st} (None=conn-reset; should be clean 400 after drain fix)")
if not os.path.exists(os.path.join(DATA, "..", "evil")) and not os.path.exists("/opt/omi-audio/evil"):
    ok("traversal wrote nothing outside data dir")
else:
    bad("traversal escape", "file created outside data dir!")

# 5. routing: two uids -> distinct folders
small = make_pcm(seconds=1)
post("verify_a", small)
post("verify_b", small)
da = os.path.join(DATA, "verify_a")
db = os.path.join(DATA, "verify_b")
if os.path.isdir(da) and os.path.isdir(db) and da != db and list_files("verify_a") and list_files("verify_b"):
    ok("two uids -> distinct folders")
else:
    bad("routing distinct folders", f"a={os.path.isdir(da)} b={os.path.isdir(db)}")

print()
print(f"RESULT: {'ALL PASS' if not fails else 'FAILURES: ' + ', '.join(fails)}")
sys.exit(1 if fails else 0)
