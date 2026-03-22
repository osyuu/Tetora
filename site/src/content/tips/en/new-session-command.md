---
title: "Starting Fresh Sessions"
lang: en
date: "2026-03-20"
excerpt: "Use tetora session new to start a clean agent session without accumulated context."
---

Over time, agent sessions accumulate context that can slow down responses or cause confusion. The `tetora session` commands help you manage this.

## Create a New Session

```bash
tetora session new --agent kokuyou
```

This starts a fresh session for the specified agent while preserving the previous session in history.

## When to Use

- Agent seems confused or referencing old context
- Starting a completely new task unrelated to previous work
- After a major codebase change that invalidates prior context
- When you want a clean slate for benchmarking agent performance

## Session Management

```bash
tetora session list                    # List all sessions
tetora session show <id>               # View session details
tetora session switch <id>             # Switch to a previous session
tetora session delete <id>             # Delete a session
```

## Tips

- Sessions are per-agent — creating a new session for one agent doesn't affect others
- Previous sessions are searchable via `tetora history`
- Set `session.auto_rotate: true` in config to auto-create new sessions daily
