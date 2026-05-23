---
description: Pull recent session content from peer coding agents (Codex, OpenClaw, Copilot CLI) that worked on this project.
argument-hint: "[agent] [count]"
allowed-tools: ["mcp__tma1__get_peer_sessions"]
---

# TMA1 Peer-Agent Lens — `/tma1-peer`

Invoked because the user wants to see what a peer coding agent left on this
project. Full reference + examples live in `skills/tma1-peer/SKILL.md`; this
file carries the essential rules for the explicit-invocation path.

## Parse `$ARGUMENTS`

- 1st token (optional) → agent name. Normalize:
  - `codex` → `codex`
  - `openclaw` → `openclaw`
  - `copilot` / `copilot_cli` → `copilot_cli`
  - `all` / `*` / empty → `""` (all peers, server excludes the caller)
  - **Anything else** → reply `unknown peer agent "<X>"; available: codex, openclaw, copilot, all` and **STOP**.
- 2nd token (optional) → integer, default `1`, clamped to `[1, 5]` server-side.

## Call the tool

`mcp__tma1__get_peer_sessions` with:
- `agent_source`: the normalized name (or `""`)
- `limit`: parsed count
- `message_limit`: `30`

## Use the response

A JSON payload with:
- `sessions[]` — each carries `session_id` / `agent_source` / `last_activity_at` / `last_activity_ago` / `duration_minutes` / `tool_call_count` / `messages` / `recent_tool_names` / `files_touched` / `cwd`.
- `most_recent_session` — top-level shortcut with the freshest peer's `agent_source` + `last_activity_ago`; use it for your first-line summary so the user immediately knows whether the peer work is current.
- `partial_failures` — `agent → error` map, present **only** when one or more peer queries failed in the all-peers fan-out. Check this before treating empty `sessions` as silence.
- `note` — present only when `sessions` is empty AND no partial failures.

Rules:
- **Quote concrete points the peer made.** Do not paraphrase. The user uses this command specifically to read the peer's exact words, not your summary.
- List `files_touched` if the next step is "fix what they flagged".
- Empty `sessions` **with no `partial_failures`** → reply `no recent <agent> sessions on this project in the active window` and stop.
- Empty `sessions` **with `partial_failures`** → tell the user which peer(s) failed (quote the error) instead of asserting silence. Don't fabricate.
