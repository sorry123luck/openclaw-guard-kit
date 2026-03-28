## OpenClaw Guard Rules

Protected files:

- `~/.openclaw/openclaw.json`
- `~/.openclaw/agents/<agentId>/agent/auth-profiles.json`
- `~/.openclaw/agents/<agentId>/agent/models.json`

Rules:

1. If the task can be reduced to writing final content into one known protected file, use `guard.exe guarded-write`.
2. Use `guard.exe openclaw-op` only when the task must invoke an OpenClaw control-plane write operation itself.
3. Never use pause-monitoring, resume-monitoring, restart, start, or stop as the normal path for protected config edits.
4. If guard rejects, validation fails, or guard returns rollback / self-heal / candidate results, stop and treat guard as authoritative.
5. Never manually skip candidate -> verify -> promote/rollback. Do not use restart, pause-monitoring, resume-monitoring, or baseline refresh to force a candidate change to become trusted.