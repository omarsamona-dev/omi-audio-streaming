#!/bin/sh
set -u
INTERVAL="${SWEEP_INTERVAL_SEC:-300}"
echo "omi-audio-compactor up: interval=${INTERVAL}s bitrate=${OPUS_BITRATE:-24k} min_age=${MIN_AGE_MIN:-2}m retention_days=${RETENTION_DAYS:-0}"
while :; do
  /usr/local/bin/sweep.sh || echo "sweep errored, continuing" >&2
  sleep "$INTERVAL"
done
