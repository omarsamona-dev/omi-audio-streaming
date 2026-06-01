# omi-audio-streaming (local-folder sink)

Webhook receiver for the **stock Omi iOS app** "Realtime audio bytes" feature
(Settings → Developer Mode). It authenticates the request, accepts the raw
PCM16 mono stream, wraps each chunk in a self-contained WAV header, and writes
it to a local folder on the VPS.

Forked from [`mdmohsin7/omi-audio-streaming`](https://github.com/mdmohsin7/omi-audio-streaming);
the Google Cloud Storage sink was replaced with a local-folder sink and token
auth + uid allow-list were added. Pure Go stdlib, no dependencies.

> Transcription stays in the Omi app (Omar's own Deepgram key). Pulling
> transcripts into Moxie Mesh is a separate, later card — **not** built here.

## How Omi posts

```
POST {url}?token={SECRET}&sample_rate=16000&uid={uid}
Content-Type: application/octet-stream
Body: raw PCM16 mono bytes (sent every N seconds)
```

Omi CV1 streams at `sample_rate=16000`. The shared secret **must** live in the
URL because the Omi app only lets you set a URL (no custom headers).

## Endpoints

| Method | Path        | Notes                                                   |
|--------|-------------|---------------------------------------------------------|
| POST   | `/audio`    | Auth + accept stream → write WAV. See status codes.     |
| GET    | `/healthz`  | Liveness probe → `200 ok`.                              |

### `/audio` behavior

- **401** if `token` != `CAPTURE_TOKEN` (constant-time compare). No file written.
- **400** if `uid` missing/invalid (must match `^[A-Za-z0-9_-]{1,128}$` — blocks
  path traversal) or `sample_rate` out of `[8000,192000]`.
- **403** if `OMI_ALLOWED_UIDS` is set and `uid` is not in it. No file written.
- **200** otherwise → file written at `{AUDIO_DIR}/{uid}/{YYYY-MM-DD}/{unix_ts_ms}.wav`
  (UTC date). Empty body → 200, no file.

The token is **never** logged. Each successful write logs `{uid, bytes, sample_rate, path}`.

## Output files

```
{AUDIO_DIR}/{uid}/{YYYY-MM-DD}/{unix_ts_ms}.wav
```

One self-contained `.wav` per received chunk (PCM16, mono, `sample_rate` from
the query param). Per-chunk files are intentional for v1: simplest and
crash-safe (no growing file to corrupt).

## Environment variables

| Var                | Required | Default | Notes                                            |
|--------------------|----------|---------|--------------------------------------------------|
| `CAPTURE_TOKEN`    | yes      | —       | Shared secret. App refuses to start if unset.    |
| `AUDIO_DIR`        | no       | `/data` | Root output folder (bind-mounted on the VPS).    |
| `OMI_ALLOWED_UIDS` | no       | —       | Comma-separated uid allow-list. Unset = allow any.|
| `PORT`             | no       | `8080`  | HTTP listen port.                                |

## Deploy (VPS, standalone + Traefik)

Runs as a standalone container on the `coolify` docker network; Traefik routes
`omi-audio.moxiemesh.com` to it (TLS via Let's Encrypt). This matches the
proven `lifeos` / `moxie-mesh-mcp` deploy pattern on this VPS.

```bash
# on the VPS, in /opt/omi-audio
sudo docker compose up -d --build
```

`.env` (not committed) holds `CAPTURE_TOKEN` (and optionally `OMI_ALLOWED_UIDS`).
The host folder `/opt/omi-audio/data` is bind-mounted to `/data`.

## Omi app setup (done by Omar, by hand, after deploy)

Settings → Developer Mode → **Realtime audio bytes**:

```
URL:      https://omi-audio.moxiemesh.com/audio?token={CAPTURE_TOKEN}
Interval: 10
```

## Disk note

Raw PCM16 mono @16kHz WAV is uncompressed: **~115 MB/hour** (~30+ GB/month of
all-day wear). The receiver ships WAV to prove the path; the **compaction
sidecar** (below) shrinks it ~10× and bounds growth.

## Compaction sidecar (CC-PROMPT-104)

A separate `omi-audio-compactor` container runs a timed sweep that re-encodes
each `*.wav` to a sibling `*.opus` (~24 kbps, ~11 MB/hour — about 10× smaller),
then deletes the WAV. It runs **independently of the receiver** so an ffmpeg
fault can never block or back up the webhook hot path. It only shares the
`./data` volume — no ports, no network, no public surface.

**No-data-loss guard (non-negotiable):** a `.wav` is deleted *only* after a
verified-good `.opus` exists for it — the encode must succeed, the temp file
must be non-empty, and the Opus duration must be within 0.25 s of the source.
A failed/empty/short encode keeps the WAV and leaves no `.part` behind.
The sweep is idempotent and restart-safe (existing `.opus` siblings are skipped).

Opus is lossy; that's accepted — transcription happens live in the Omi app
(BYOK Deepgram). The VPS copy is an **archive**, not the transcription source.

### Env knobs (set on the `omi-audio-compactor` service)

| Var                 | Default | Meaning                                                        |
|---------------------|---------|----------------------------------------------------------------|
| `OPUS_BITRATE`      | `24k`   | Opus target bitrate.                                           |
| `MIN_AGE_MIN`       | `2`     | In-flight guard: skip WAVs newer than this (still streaming).  |
| `SWEEP_INTERVAL_SEC`| `300`   | Seconds between sweeps.                                        |
| `RETENTION_DAYS`    | `0`     | **0 = retention OFF.** >0 = delete `.opus`/`.wav` older than N days + prune empty dirs. |

At 24 kbps Opus the ~30 GB/mo WAV rate drops to ~3 GB/mo — over a year of
runway at current free space — so retention ships **disabled**.

### Enable retention later

```bash
# on the VPS, in /opt/omi-audio — keep RETENTION_DAYS in the compose env
sudo sed -i 's/RETENTION_DAYS=0/RETENTION_DAYS=30/' docker-compose.yml
sudo docker compose up -d --build omi-audio-compactor   # recreate just the sidecar
```

### Trigger a sweep on demand

```bash
sudo docker exec omi-audio-compactor /usr/local/bin/sweep.sh
```
