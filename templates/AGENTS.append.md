# Guard workflow rules for AGENTS.md

- Before modifying openclaw.json, the agent must start guard.exe.
- Before modifying uth-profiles.json in auth-related tasks, the agent must start guard.exe.
- The agent must not directly execute OpenClaw gateway restart/start/stop when guard flow is required.
- During guard window, the user should be told not to continue issuing instructions until the guard flow finishes.
