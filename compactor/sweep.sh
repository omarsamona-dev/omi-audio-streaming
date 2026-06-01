#!/bin/sh
# omi-audio compaction sweep: WAV -> Opus (verified) + optional retention.
# Never deletes a WAV without a verified-good Opus. Idempotent & restart-safe.
set -u

DATA_DIR="${DATA_DIR:-/data}"
OPUS_BITRATE="${OPUS_BITRATE:-24k}"
MIN_AGE_MIN="${MIN_AGE_MIN:-2}"        # in-flight guard: skip files newer than this
RETENTION_DAYS="${RETENTION_DAYS:-0}"  # 0 = retention disabled (documented fallback)

# --- Compaction pass ---
find "$DATA_DIR" -type f -name '*.wav' -mmin +"$MIN_AGE_MIN" 2>/dev/null | while IFS= read -r wav; do
  opus="${wav%.wav}.opus"
  [ -f "$opus" ] && continue
  tmp="${opus}.part"
  # -f opus is REQUIRED: the temp name ends in `.part`, so ffmpeg cannot infer
  # the muxer from the extension and would fail with "Unable to choose an
  # output format". Forcing the Ogg-Opus muxer makes the .part name irrelevant.
  if ffmpeg -nostdin -y -i "$wav" -c:a libopus -b:a "$OPUS_BITRATE" -ac 1 -ar 16000 -f opus "$tmp" 2>/dev/null; then
    src=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$wav" 2>/dev/null || echo 0)
    out=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$tmp" 2>/dev/null || echo 0)
    if [ -s "$tmp" ] && awk -v a="$src" -v b="$out" 'BEGIN{d=a-b; if(d<0)d=-d; exit !(a>0 && b>0 && d<=0.25)}'; then
      mv -f "$tmp" "$opus" && rm -f "$wav" && echo "compacted: $wav"
    else
      rm -f "$tmp"; echo "VERIFY FAILED (kept WAV): $wav src=$src out=$out" >&2
    fi
  else
    rm -f "$tmp"; echo "ENCODE FAILED (kept WAV): $wav" >&2
  fi
done

# --- Retention pass (only when RETENTION_DAYS > 0) ---
if [ "${RETENTION_DAYS:-0}" -gt 0 ] 2>/dev/null; then
  find "$DATA_DIR" -type f \( -name '*.opus' -o -name '*.wav' \) -mtime +"$RETENTION_DAYS" -delete 2>/dev/null
  find "$DATA_DIR" -mindepth 1 -type d -empty -delete 2>/dev/null
fi
