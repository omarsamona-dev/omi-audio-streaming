#!/bin/bash
# SessionStart hook — makes Claude Code on the web able to do omi-audio VPS
# recon/transcription work (CC-PROMPT-142 and successors).
#
# It installs the two tools the base web container lacks and materializes the
# Hostinger VPS SSH key from a secret. It does NOT and CANNOT open network
# egress to the VPS — that is a network-policy setting chosen when the
# environment is created. See .claude/REMOTE-VPS-ACCESS.md for the full
# checklist. Local dev machines are skipped (they already have these tools/keys).
set -euo pipefail

# Web (remote) sessions only.
if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

# 1) Tooling: ssh client (VPS recon) + ffmpeg/ffprobe (.opus -> 16k mono wav
#    for the Groq Whisper path). Idempotent: skip if both already present.
if ! command -v ssh >/dev/null 2>&1 || ! command -v ffmpeg >/dev/null 2>&1; then
  # `apt-get update` may exit non-zero on unrelated third-party PPAs; the main
  # Ubuntu archive still refreshes, which is all we need.
  sudo apt-get update -qq || true
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-client ffmpeg
fi

# 2) Materialize the Hostinger VPS private key from an environment secret.
#    Set HOSTINGER_VPS_KEY (full private-key text) in the environment's secrets.
#    We never print it. Connect with:
#      ssh -i ~/.ssh/hostinger_vps -o StrictHostKeyChecking=accept-new \
#          root@31.97.143.52
if [ -n "${HOSTINGER_VPS_KEY:-}" ] && [ ! -f "$HOME/.ssh/hostinger_vps" ]; then
  mkdir -p "$HOME/.ssh"
  chmod 700 "$HOME/.ssh"
  printf '%s\n' "$HOSTINGER_VPS_KEY" > "$HOME/.ssh/hostinger_vps"
  chmod 600 "$HOME/.ssh/hostinger_vps"
fi
