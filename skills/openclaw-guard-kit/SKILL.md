---
name: openclaw-guard-kit
description: Use OpenClaw Guard Kit before changing protected OpenClaw config or protected per-agent state.
---

# OpenClaw Guard Kit

Use this skill before changing protected OpenClaw configuration or protected per-agent state.

## Protected files

- `~/.openclaw/openclaw.json`
- `~/.openclaw/agents/<agentId>/agent/auth-profiles.json`
- `~/.openclaw/agents/<agentId>/agent/models.json`

## Default path

Use `guard.exe guarded-write` when the task can be reduced to writing final content into one known protected file.

This is the default path for:
- replacing `openclaw.json` with prepared final content
- replacing `auth-profiles.json` with prepared final content
- replacing `models.json` with prepared final content

Flow:
1. Prepare the final file content.
2. Run `guard.exe guarded-write`.
3. Let guard complete or reject the write.
4. Stop if guard rejects or validation fails.

## OpenClaw control-plane path

Use `guard.exe openclaw-op` only when the task must invoke an OpenClaw control-plane write operation itself.

Typical examples:
- `openclaw config set`
- `openclaw gateway call config.patch`
- `openclaw gateway call config.apply`

Use this path only when the operation must preserve OpenClaw's own command semantics, such as built-in validation, normalization, restart behavior, or other command-side effects.

## Do not do these things

- Do not edit protected files directly without guard.
- Do not use pause-monitoring or resume-monitoring as the normal path for protected config edits.
- Do not use restart, start, or stop as a shortcut around protected config changes.
- Do not continue after guard rejects or validation fails.

## Practical rule

- One known protected file + known final content -> `guard.exe guarded-write`
- Must invoke OpenClaw control-plane write command itself -> `guard.exe openclaw-op`
- Guard reports rollback / self-heal / candidate / validation issue -> stop and follow guard
- Never manually skip candidate -> verify -> promote/rollback, and do not use restart, pause-monitoring, resume-monitoring, or baseline refresh to force candidate content to become trusted