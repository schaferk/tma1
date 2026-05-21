# Changelog

## v2.0 — Closing the agent loop

v1 made what your agent did visible. v2 makes that visibility actionable
by feeding it back into the agent's reasoning loop.

### Highlights

- **Five Claude Code hooks** registered automatically by
  `tma1-server install --adapter claude-code`: `UserPromptSubmit`
  prepends a session digest before each turn, `SessionStart` orients
  fresh sessions with prior state, `PreCompact` carries critical state
  through context compaction, `PostToolUse` injects per-tool anomaly
  notes, and `Stop` blocks termination on unresolved
  high-severity issues.
- **Seven MCP stdio tools**: `get_context_bundle`, `get_session_state`,
  `get_anomalies`, `get_build_status`, `get_external_changes`,
  `get_project_state`, `get_peer_sessions`. The MCP server is the same
  `tma1-server` binary, invoked via `mcp-serve`.
- **Six anomaly rules** running on every `Detect`, with a 10-minute
  per-session suppression layer + per-rule resolution checks. Each
  anomaly routes through a `Channel` (`user_prompt_submit`,
  `stop_block`) so the same finding never injects twice.
- **`/tma1-peer` slash command** reads what Codex / OpenClaw /
  Copilot CLI just left on this project and brings it into Claude's
  context — no copy-paste between terminals.
- **Three new sensors** populate the perception bundle:
  - Build sensor — wraps build / dev commands and captures output to
    `tma1_build_events` (`tma1-server build [--watch] -- <cmd>`).
  - Git/file sensor — fsnotify + periodic `git log -1` poll, classifies
    changes as `human` or `agent` and writes to `tma1_external_changes`.
  - Project sensor — language / build-system / structural index,
    refreshed daily into `tma1_project_state`.

### One-shot adapter install

```bash
# macOS / Linux
curl -fsSL https://tma1.ai/install.sh | TMA1_ADAPTER=claude-code bash

# Windows
$env:TMA1_ADAPTER = 'claude-code'; irm https://tma1.ai/install.ps1 | iex
```

This wires hook entries into `~/.claude/settings.json`, registers
TMA1 as an MCP server in `~/.claude.json`, drops the `/tma1-peer`
skill into `~/.claude/skills/`, and adds a TMA1 block to your
project's CLAUDE.md (or AGENTS.md). All idempotent — repeat install
only updates what's stale.

### New HTTP endpoints

| Endpoint | Use |
|----------|-----|
| `/api/hooks` | Hook event ingest (request-response, returns injection content) |
| `/api/hooks/stream` | SSE feed of hook events for the live agent canvas |
| `/api/anomalies` | Recent anomalies across sessions |
| `/api/anomalies/budget` | Daily emit count per Kind (1.7 gate ≤ 5 / day) |
| `/api/anomalies/follow-rate` | Did the agent take the suggested action within N tool calls? (1.7 gate ≥ 30%) |

### New GreptimeDB tables

| Table | Source |
|-------|--------|
| `tma1_build_events` | Build sensor (`tma1-server build`) |
| `tma1_external_changes` | Git/file sensor |
| `tma1_project_state` | Project sensor |
| `tma1_anomaly_emits` | Suppression layer logs every emitted anomaly |

The 27 hook event types (`tma1_hook_events`) and conversation table
(`tma1_messages`) from v1 are unchanged. `tool_input` derived columns
(`tool_file_path`, `tool_command_prefix`, `tool_success`,
`tool_error_summary`) are extracted at ingest so anomaly rules don't
have to `regexp_match` the raw blob.

### Configuration additions

| Variable | Default | Effect |
|----------|---------|--------|
| `TMA1_ADAPTER` | (empty) | Wire an agent during install (`claude-code`). Idempotent. |
| `TMA1_DISABLE_INJECTION` | (unset) | When `1`, hook handlers return empty bodies. |
| `TMA1_CONTEXT_PRESSURE_THRESHOLD` | `100000` | Token threshold for `context_pressure` anomaly. |

### Compatibility

Every v1 dashboard view, every v1 endpoint, every v1 table, and the
entire OTel proxy path are unchanged. v2 is additive: if you don't
install an adapter, TMA1 still works as the v1 local-first
observability tool with no behaviour change.

### Documentation

- [docs/mcp-tools.md](docs/mcp-tools.md) — the seven MCP tools
- [docs/hooks.md](docs/hooks.md) — the five hook events, injection
  protocol, fail-safe contract, runtime invariants
- [docs/anomalies.md](docs/anomalies.md) — the six rules, channels,
  suppression, resolution, validation gates
