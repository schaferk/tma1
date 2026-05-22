# Hooks

TMA1 wires into Claude Code via five hook events (Codex registers
four of them — Codex's hook catalogue does not include `PreCompact`).
Each event is a request-response exchange: the hook script POSTs the
event payload to `http://127.0.0.1:14318/api/hooks`, the server's
stdout-shaped reply becomes the injection content.

## The injection events

| Event | When the agent fires it | What TMA1 returns | Channel | Adapters |
|-------|-------------------------|-------------------|---------|----------|
| `UserPromptSubmit` | Before each user turn | `<tma1-context>` digest (session state, anomalies, build, recent external changes) | Prepended to the user prompt | CC + Codex |
| `PostToolUse` | After each tool call returns | One-line anomaly note when a rule routes to `post_tool_use` (no rule does today; default silent) | Appended to tool result | CC + Codex |
| `Stop` | When the agent wants to stop | JSON `{"decision":"block","reason":"…"}` when there are unresolved `stop_block`-channel anomalies; empty otherwise | Blocks termination | CC + Codex |
| `SessionStart` | New session opens | `<tma1-context>` digest for the prior session + external changes in the meantime | Prepended to the session's first prompt | CC + Codex |
| `PreCompact` | Before CC compacts old turns | "Preserve through compaction" framed bundle | Folded into the post-compaction summary | CC only — Codex has no equivalent hook |

## Hook script protocol

`tma1-server install --adapter claude-code` writes
`~/.tma1/hooks/tma1-hook.sh` (or `.ps1` on Windows). For Codex,
`tma1-server install --adapter codex` writes the analogous
`~/.tma1/hooks/tma1-hook-codex.sh` (or `.ps1`). Both scripts:

1. Reads the event JSON from stdin.
2. POSTs it to `127.0.0.1:14318/api/hooks`.
   - **Unix**: `curl -sf -m 0.5` — 500 ms hard cap.
   - **Windows**: `Invoke-WebRequest -TimeoutSec 1` — 1 s cap.
     PowerShell's `-TimeoutSec` only accepts whole seconds, so we
     trade ~500 ms of latency on Windows for keeping the script
     dependency-free (no .NET HttpClient wrapper).
   - **Codex variant** appends `?source=codex&envelope=codex` so
     the handler tags the row with `agent_source='codex'` and
     reshapes the response from CC's raw-stdout / Stop-decision-JSON
     into Codex's `hookSpecificOutput.additionalContext` shape (Stop
     stays as `{decision,reason}` — that shape is identical for both
     agents). The hook-content generator
     (`generateInjection`) is shared; only the envelope differs.
3. Writes the response body to stdout.

On any error — server unreachable, timeout, non-200, etc. — stdout is
empty and exit code is 0. **The hook never blocks the agent**: a dead
TMA1 means no injection, not a stuck CC.

The server side mirrors that contract: `handleHooks` writes to
GreptimeDB asynchronously (fire-and-forget goroutine) and synchronously
returns the injection body, capped by `hookInjectionTimeout = 300 ms`
inside `generateInjection`. Both client timeouts above sit above that
cap, so a slow path falls back to "no injection" rather than blocking
the agent.

## Registration

Both adapters write idempotent entries scoped to a `tma1`
identifier so legacy and user-written entries for the same events
are preserved.

**Claude Code** — `install --adapter claude-code` writes into
`~/.claude/settings.json` for all five injection-content events
(plus the other 22 telemetry-only events Claude Code emits):

```json
{
  "hooks": {
    "UserPromptSubmit": [{ "id": "tma1", "matcher": "", "hooks": [...] }],
    "PostToolUse":      [{ "id": "tma1", "matcher": "", "hooks": [...] }],
    "Stop":             [{ "id": "tma1", "matcher": "", "hooks": [...] }],
    "SessionStart":     [{ "id": "tma1", "matcher": "", "hooks": [...] }],
    "PreCompact":       [{ "id": "tma1", "matcher": "", "hooks": [...] }]
  }
}
```

The installer recognises legacy entries (no `id` field) by command path
and rewrites them in place — it won't add a second TMA1 entry that runs
the same script twice.

**Codex** — `install --adapter codex` writes the analogous entries
into `~/.codex/hooks.json` for the four injection events Codex
supports (`SessionStart`, `UserPromptSubmit`, `PostToolUse`, `Stop`)
plus `PreToolUse` for anomaly telemetry — Codex's hook catalogue
([developers.openai.com/codex/hooks](https://developers.openai.com/codex/hooks))
has no `PreCompact` event, so context compaction is the one signal
the Codex adapter cannot push. It also registers
itself as an MCP server under `[mcp_servers.tma1]` in
`~/.codex/config.toml` (TOML merge so user-managed entries stay
intact), and drops the `tma1-peer` skill into
`~/.agents/skills/tma1-peer/`. Stale-sweep on these multi-tenant
directories is scoped to the `tma1-` owner prefix — user-installed
skills sitting alongside ours are never touched.

The hook **content** is the same (`generateInjection` is
adapter-agnostic). Only the **envelope** differs: Codex's hook
script POSTs with `?envelope=codex`, and the server reshapes the
response into Codex's `hookSpecificOutput.additionalContext`. Stop
output already matches both agents (`{decision:"block",reason:…}`).

`PreToolUse` on Codex deliberately never injects context — Codex
does not consume `additionalContext` from `PreToolUse` hooks
([openai/codex#19385](https://github.com/openai/codex/issues/19385)).
The shaper emits `{"continue": true}` for PreToolUse and lets the
agent proceed. Our anomaly rules route HIGH-severity findings
through `Stop` and MEDIUM through `UserPromptSubmit`, both of
which Codex consumes correctly, so this gap doesn't affect the
rules we ship.

### Why every matcher is `""`

The plan calls for `PostToolUse` to register with
`matcher: "Edit|Write|Bash|Read"`. The shipped installer uses `""`
(all tools) for every event and pushes the dispatch decision to the
server. The trade-off:

- **Pro**: extending support to a new tool — e.g. a future
  `WebFetch` rule — needs no change to the user's `settings.json`. The
  server-side rule decides what's interesting; no client-side config
  drift.
- **Con**: every tool's PostToolUse fires a hook event the server
  promptly returns empty for. The script costs a curl + JSON ingest +
  empty response — typically < 5 ms on localhost, but it does show up
  in `hookTelemetry` flush logs as the per-event call count.

When the cost becomes a real concern (e.g. heavy `Read` storms), the
right fix is filtering server-side in `generateInjection` rather than
narrowing matchers — keeps the data path and the dispatch logic in one
place.

## Important runtime details

### Stop hook loop guard

CC re-fires `Stop` after a block with `stop_hook_active: true`. If TMA1
ignored that flag it would block the agent again and form an infinite
loop (`work → Stop → block → work → Stop → block → …`). The server
parses the event JSON and short-circuits to empty stdout when
`stop_hook_active == true`.

### PostToolUseFailure derivation

CC does not emit a native `PostToolUseFailure` event. The server
inspects `tool_response` on every `PostToolUse` and rewrites
`event_type` to `PostToolUseFailure` when any of these markers are
present, before writing to `tma1_hook_events`:

- `isError: true` / `is_error: true`
- `success: false`
- `interrupted: true`
- `error` field is a non-empty string
- `code` / `exitCode` is a non-zero number

This is the only place the synthetic kind enters the data path for
native CC hooks. Anomaly rules query `event_type =
'PostToolUseFailure'` directly.

### MCP stdout invariant

`tma1-server mcp-serve` redirects all logging to stderr before
starting any subsystem. Stdout is reserved for JSON-RPC frames; any
write there breaks the protocol.

### Opt-out

Set `TMA1_DISABLE_INJECTION=1` on the `tma1-server` process to make all
hook responses empty. CC keeps firing hooks (the script still POSTs,
the server still writes events to GreptimeDB), but nothing is injected
into the agent's context.

### Opt-in: `.tma1-context.md` file callback

The plan lists a `.tma1-context.md` "file callback" as the fallback
for non-MCP agents (Aider, Cursor) that read context via their own
Read tool rather than via MCP. The implementation ships it but
**leaves it off by default**, gated behind `TMA1_ENABLE_FILE_CALLBACK=1`.

Why off: dogfooding showed it was net-negative for MCP-equipped users
— writing the file on every hook fired the git sensor's own watcher,
producing self-noise the attribution layer then had to filter back
out. Set the env var when running a CC-less agent that genuinely needs
the file.

### Cache invalidation on every event

Every hook event invalidates the per-session anomaly cache so the next
`Detect` re-runs against fresh data. This is why a Read of a stale
file shows up as an anomaly on the very next turn rather than 30
seconds later.

### Project sensor latency on SessionStart

The project sensor's `Index(cwd)` is fire-and-forget on regular
events. `SessionStart` uses `IndexAndWait(cwd, 300ms)` instead so the
subsequent bundle query actually sees the just-written
`tma1_project_state` row — without the bounded synchronous wait the
agent gets an empty project state on cold sessions.

### Git sensor attachment is async

The git sensor's `Observe(cwd)` reserves the watcher slot synchronously
but defers the actual `fsnotify` recursive walk to a goroutine. On a
large monorepo the walk can take 100 ms+; the hook hot path must not
block on it.
