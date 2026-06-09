# Letting Claude Code on the web reach the omi-audio VPS

Recon/transcription tasks (e.g. CC-PROMPT-142 — verifying a capture window in
`/opt/omi-audio/data`) require this remote session to reach the VPS. The base
web container **cannot** by default: no egress to the VPS, no SSH key, no
`ssh`/`ffmpeg`. Three things must line up; only #3 is automated by the
SessionStart hook.

## 1. Network policy (you set this when creating/editing the environment)
Allow outbound TCP to `31.97.143.52:22`. The default policies block it — pick a
custom allowlist that includes the VPS (and keep the Ubuntu package archive
reachable so the hook can install tooling). Docs:
https://code.claude.com/docs/en/claude-code-on-the-web

## 2. Secrets (you add these to the environment config)
- `HOSTINGER_VPS_KEY` — full text of the `~/.ssh/hostinger_vps` private key.
- `GROQ_API_KEY` — for the Whisper `large-v3` transcription path.

## 3. Tooling + key file (automated by `.claude/hooks/session-start.sh`)
On each web session the hook installs `openssh-client` + `ffmpeg` and writes
`HOSTINGER_VPS_KEY` to `~/.ssh/hostinger_vps` (chmod 600). Nothing secret is
printed.

## Verify after the environment restarts
```bash
ssh -i ~/.ssh/hostinger_vps -o StrictHostKeyChecking=accept-new \
    root@31.97.143.52 'docker ps --filter name=omi-audio --format "{{.Names}} {{.Status}}"'
```
A line naming `omi-audio` means recon can proceed. A hang/timeout means the
network policy (#1) still blocks port 22.
