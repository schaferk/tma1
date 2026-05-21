# Hooks

TMA1 wires into Claude Code via five hook events. Each one is a
request-response exchange: the hook script POSTs the event payload to
`http://127.0.0.1:14318/api/hooks`, the server's stdout-shaped reply
becomes the injection content.

## The five events

| Event | When CC fires it | What TMA1 returns | Channel |
|-------|------------------|-------------------|---------|
| `UserPromptSubmit` | Before each user turn | `<tma1-context>` digest (session state, anomalies, build, recent external changes) | Prepended to the user prompt |
| `PostToolUse` | After each tool call returns | One-line anomaly note when a rule routes to `post_tool_use` (no rule does today; default silent) | Appended to tool result |
| `Stop` | When the agent wants to stop | JSON `{"decision":"block","reason":"…"}` when there are unresolved `stop_block`-channel anomalies; empty otherwise | Blocks termination |
| `SessionStart` | New session opens | `<tma1-context>` digest for the prior session + external changes in the meantime | Prepended to the session's first prompt |
| `PreCompact` | Before CC compacts old turns | "Preserve through compaction" framed bundle | Folded into the post-compaction summary |

## Hook script protocol

`tma1-server install --adapter claude-code` writes
`~/.tma1/hooks/tma1-hook.sh` (or `.ps1` on Windows). The script:

1. Reads the event JSON from stdin.
2. POSTs it to `127.0.0.1:14318/api/hooks` with `curl -m 0.5`.
3. Writes the response body to stdout.

On any error — server unreachable, timeout, non-200, etc. — stdout is
empty and exit code is 0. **The hook never blocks the agent**: a dead
TMA1 means no injection, not a stuck CC.

The server side mirrors that contract: `handleHooks` writes to
GreptimeDB asynchronously (fire-and-forget goroutine) and synchronously
returns the injection body. If the synchronous half is slow, the curl
timeout fires and the agent runs without injection.

## Registration

`install --adapter claude-code` writes idempotent entries into
`~/.claude/settings.json` for all five events:

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
