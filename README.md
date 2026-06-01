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
all-day wear). v1 ships WAV to prove the path. Follow-up (separate card):
encode to Opus on ingest (~10× smaller) **or** a retention cron.
