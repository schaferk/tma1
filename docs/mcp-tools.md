# MCP tools

TMA1 exposes seven MCP stdio tools so the agent can pull perception data
on demand. The MCP server is the same `tma1-server` binary, invoked as:

```
tma1-server mcp-serve
```

It speaks JSON-RPC over stdin/stdout. The server is registered in
`~/.claude.json` by `tma1-server install --adapter claude-code`; agents
spawn one MCP process per session.

Two operational notes:

- The `mcp-serve` entrypoint redirects all logging to stderr — stdout is
  reserved for JSON-RPC frames. Anything that writes to stdout corrupts
  the protocol; if you fork the server, keep that invariant.
- `mcp-serve` does NOT spawn its own GreptimeDB. It connects to the
  parent `tma1-server` process's database on
  `TMA1_GREPTIMEDB_HTTP_PORT` (default 14000). Make sure `tma1-server`
  is running before spawning MCP sessions.

## get_context_bundle

Aggregate entry point. Returns project name, current session state,
active anomalies (UserPromptSubmit-channel only), build status, recent
external changes, and project structure — the same payload the
`UserPromptSubmit` hook injects.

| Arg | Type | Default | Description |
|-----|------|---------|-------------|
| `session_id` | string | latest for cwd | Override the resolved session |
| `cwd` | string | server cwd | Project root for resolution |

Call this when context feels stale — after a compaction, when you've
switched directories, or as a "what does TMA1 know right now" probe.

## get_session_state

Full state for one session: tool history aggregates, token usage,
current focus, recent files, last build error, external human changes
during the session.

| Arg | Type | Default | Description |
|-----|------|---------|-------------|
| `session_id` | string | active session for cwd | Session to inspect |
| `verbose` | boolean | false | When true, include a chronological `actions` array of recent PreToolUse / PostToolUse / PostToolUseFailure entries |
| `action_limit` | integer | 50 | Cap on the verbose action list (clamped to 1-200). Ignored when verbose is false |

The verbose variant is the Phase 0.1 "raw action list" channel (it
folds in what was originally proposed as a separate `get_recent_actions`
tool). Each action carries `ts`, `event_type`, `tool_name`,
`file_path` (when applicable), `command_prefix` (Bash / exec_command),
and `success` (only on PostToolUseFailure — `true` on PostToolUse,
absent on PreToolUse).

## get_anomalies

List anomalies for one session, already routed through suppression so
re-emits within the 10-minute silence window are absent.

| Arg | Type | Default | Description |
|-----|------|---------|-------------|
| `session_id` | string | active session for cwd | Session to inspect |

Each anomaly carries `kind`, `severity`, `channel`, `evidence`,
`suggestion`, `related_files`, `first_emitted_at`. See
[anomalies.md](anomalies.md) for the kinds.

## get_build_status

Most recent build / dev output captured by the build sensor
(`tma1-server build --watch -- <cmd>`).

| Arg | Type | Default | Description |
|-----|------|---------|-------------|
| `tag` | string | most recent | Build watcher tag |

Returns the last error message and timestamp, plus a stale flag when
the last error is older than the build watcher's idle threshold (so
the agent doesn't act on a stale failure).

## get_external_changes

Files modified outside the agent loop, plus git commits and branch
moves, classified as `human` or `agent` attribution.

| Arg | Type | Default | Description |
|-----|------|---------|-------------|
| `since_min` | integer | 30 | Lookback window in minutes |

Useful when the agent is about to edit something. Combined with
`get_session_state.recent_files`, it answers "did anyone else touch
this file since I last read it?"

## get_project_state

Indexed project structure: language, build system, top-level
directories, key files (README, CLAUDE.md, etc). Refreshed once per
day or on demand via the project sensor.

No arguments. Resolves the project from the calling cwd.

Read this once at the start of a fresh session in a new repo before
running ls/cat/grep — the index already knows the language, build
command, and where the test files live.

## get_peer_sessions

Recent sessions on the same project from peer coding agents (Codex,
OpenClaw, Copilot CLI). The caller is assumed to be Claude Code;
`claude_code` is excluded from the result set.

| Arg | Type | Default | Description |
|-----|------|---------|-------------|
| `agent_source` | string | "" (all peers) | `codex` / `openclaw` / `copilot_cli` |
| `project` | string | derived from cwd | Absolute path = prefix match; bare name = legacy basename LIKE |
| `limit` | integer | 1 | Sessions per agent (1-5). With empty `agent_source`, applied per peer agent, not globally |
| `message_limit` | integer | 20 | Messages per session (1-100) |
| `since_min` | integer | 1440 | Lookback window in minutes (default 24h) |

The slash command `/tma1-peer [agent] [count]` is a thin wrapper around
this tool. See `claude-plugin/skills/tma1-peer/SKILL.md` for the full
argument-parsing contract that the skill ships with.
