---
name: tma1-peer
description: "Pull recent session content from peer coding agents (Claude Code, OpenClaw, Copilot CLI) that worked on the same project. Invoke this skill when the user asks you to read another agent's review feedback, see what someone else tried, or act on cross-agent context. Trigger phrases: \"what did claude do\", \"what did openclaw do\", \"what did copilot do\", \"peer sessions\", \"cross-agent context\", \"/tma1-peer\"."
---

# TMA1 Peer-Agent Lens (Codex)

You're being invoked because the user wants to see what a peer coding agent
left on this project — typically because they used Claude Code, OpenClaw,
or Copilot CLI to review or run something and now want you to act on that
work without copy-pasting it manually.

## How to invoke this skill

This skill is auto-selected when the user mentions reading another agent's
output on the current project, or when they explicitly type
`/tma1-peer [agent] [count]`. Parse:

- First positional arg (optional): peer agent name (`claude` / `claude_code`
  / `openclaw` / `copilot` / `copilot_cli` / `all`).
- Second positional arg (optional): count of recent sessions to pull,
  integer 1-5, default 1.

If neither is supplied, treat as `all` with count 1.

## Normalize the agent name

| Input             | Canonical `agent_source` to send |
| ----------------- | -------------------------------- |
| `claude`, `cc`    | `claude_code`                    |
| `claude_code`     | `claude_code`                    |
| `openclaw`        | `openclaw`                       |
| `copilot`         | `copilot_cli`                    |
| `copilot_cli`     | `copilot_cli`                    |
| `all`, `*`, empty | `""` (returns all non-Codex peers) |

Any other value: reply
`unknown peer agent "<X>"; available: claude, openclaw, copilot, all` and
STOP — do not call the tool with an unrecognised name.

## Call the tma1 MCP tool

This project's TMA1 install registers an MCP server named `tma1`. Call
the `get_peer_sessions` tool on that server with:

| Argument        | Value                                                  |
| --------------- | ------------------------------------------------------ |
| `agent_source`  | the canonical name from the table above (or `""`)      |
| `limit`         | the parsed count, default `1`, hard cap `5`            |
| `message_limit` | `30`                                                   |

If your runtime addresses MCP tools by a prefixed name (for example
`tma1.get_peer_sessions` or `mcp_tma1_get_peer_sessions`), use whichever
convention this Codex install uses — the tool itself is the same one
backing TMA1's perception layer.

## Use the returned content directly

The response is a JSON object with a `sessions` array. Each entry has:

- Identity: `session_id`, `agent_source`, `started_at`,
  `last_activity_at`, `last_activity_ago`, `duration_minutes`, `cwd`.
- Volume: `tool_call_count`, `tokens_input`, `tokens_output`.
- Content: `messages` (chronological user / assistant / thinking),
  `recent_tool_names`, `files_touched`.

When you act on what the peer left:

- **Quote concrete points the peer made.** Don't paraphrase. The user
  uses this skill specifically because they want the peer's exact
  feedback in your context, not your summary of it.
- **List the files the peer touched.** If the user's next step is "fix
  what Claude flagged", you need to know which files matter.
- **Surface the timing.** "Claude left this 12 min ago" is more useful
  than "recently".

If `sessions` is empty, reply
`no recent <agent> sessions on this project in the active window`
and stop — do not fabricate context.

## Examples

User: "what did claude leave on this project?"
You: (call `get_peer_sessions` with `agent_source: "claude_code", limit: 1, message_limit: 30`)
You: "Claude Code reviewed `server/internal/handler/anomalies.go` 8 minutes ago and flagged three concrete issues:
1. fetchEmits has no LIMIT — body cap will truncate before the query finishes.
2. `applyForceColor` env dedup is libc-dependent.
3. anomalyCache.history map never evicts stale sessions.
Want me to address all three or pick one?"

User: `/tma1-peer copilot 2`
You: (call `get_peer_sessions` with `agent_source: "copilot_cli", limit: 2, message_limit: 30`)
You: "Copilot CLI ran two sessions on this project today — the most recent
deployed staging 20 min ago (success), the prior one 2 h ago ran the
test suite and hit a flake in `TestSemAcceptsUpToCapacity`."
