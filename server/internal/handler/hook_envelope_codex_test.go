package handler

import (
	"encoding/json"
	"testing"
)

// TestWrapInjectionEnvelopeCodexAdditionalContext: the four
// string-content events (UserPromptSubmit, PostToolUse, SessionStart,
// PreCompact) wrap their content into Codex's
// hookSpecificOutput.additionalContext shape.
func TestWrapInjectionEnvelopeCodexAdditionalContext(t *testing.T) {
	cases := []struct {
		event   string
		content string
	}{
		{"UserPromptSubmit", "<tma1-context>\nproject: foo\n</tma1-context>"},
		{"PostToolUse", "ℹ️ tma1 [HIGH] stale_file_view — Re-read auth.go"},
		{"SessionStart", "Preserve through compaction — current session state:\nproject: foo"},
		{"PreCompact", "session: abc · tools=12"},
	}
	for _, c := range cases {
		t.Run(c.event, func(t *testing.T) {
			body, ctype := wrapInjectionEnvelope("codex", c.event, c.content)
			if ctype == "" || ctype[:16] != "application/json" {
				t.Errorf("Content-Type = %q, want application/json…", ctype)
			}
			var got struct {
				HookSpecificOutput struct {
					HookEventName     string `json:"hookEventName"`
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("unmarshal: %v\nbody=%s", err, body)
			}
			if got.HookSpecificOutput.HookEventName != c.event {
				t.Errorf("hookEventName = %q, want %q", got.HookSpecificOutput.HookEventName, c.event)
			}
			if got.HookSpecificOutput.AdditionalContext != c.content {
				t.Errorf("additionalContext = %q, want %q", got.HookSpecificOutput.AdditionalContext, c.content)
			}
		})
	}
}

// TestWrapInjectionEnvelopeCodexStopPassthrough: the Stop event's
// existing decision-JSON output is the SAME shape Codex's Stop hook
// consumes, so the shaper passes it through verbatim under
// application/json.
func TestWrapInjectionEnvelopeCodexStopPassthrough(t *testing.T) {
	stopJSON := `{"decision":"block","reason":"tma1 detected 1 high-severity issue(s) — repeated_failed_build"}`
	body, ctype := wrapInjectionEnvelope("codex", "Stop", stopJSON)
	if ctype[:16] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json…", ctype)
	}
	if body != stopJSON {
		t.Errorf("Stop body should pass through verbatim.\n  got: %s\n want: %s", body, stopJSON)
	}
	// Confirm what landed actually parses with the shape Codex expects.
	var probe struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil {
		t.Fatalf("Stop body not parseable as decision JSON: %v", err)
	}
	if probe.Decision != "block" {
		t.Errorf("decision = %q, want block", probe.Decision)
	}
	if probe.Reason == "" {
		t.Error("reason should be non-empty")
	}
}

// TestWrapInjectionEnvelopeCodexEmptyContent: empty injection content
// for any event yields the silent-no-op shape `{}`. This is the path
// PreToolUse always takes (we never inject context for it) and the
// path every other event takes when no anomalies / digest changes
// are pending.
func TestWrapInjectionEnvelopeCodexEmptyContent(t *testing.T) {
	for _, event := range []string{"PreToolUse", "UserPromptSubmit", "Stop", "SessionStart", "PreCompact", "PostToolUse", "Notification"} {
		body, ctype := wrapInjectionEnvelope("codex", event, "")
		if body != "{}" {
			t.Errorf("event=%s: body = %q, want {}", event, body)
		}
		if ctype[:16] != "application/json" {
			t.Errorf("event=%s: Content-Type = %q, want application/json…", event, ctype)
		}
	}
}

// TestWrapInjectionEnvelopeUnknownFallsBackToRaw: an unrecognised
// envelope (typo, future adapter) returns the raw content with no
// content-type override. We don't 400 because that would break the
// agent's loop on a query-string typo; raw passthrough is the safe
// default.
func TestWrapInjectionEnvelopeUnknownFallsBackToRaw(t *testing.T) {
	body, ctype := wrapInjectionEnvelope("zorp", "UserPromptSubmit", "hello world")
	if body != "hello world" {
		t.Errorf("body = %q, want raw passthrough", body)
	}
	if ctype != "" {
		t.Errorf("Content-Type override = %q, want empty (preserve default)", ctype)
	}
}

// TestWrapInjectionEnvelopeCodexStopWithNonDecisionContent: if the
// Stop event somehow carries non-decision content (defensive — should
// never happen given generateStopInjection's output), the shaper
// falls through and wraps as additionalContext so the agent still
// sees the message rather than getting `{"decision":...}` it can't
// parse.
func TestWrapInjectionEnvelopeCodexStopWithNonDecisionContent(t *testing.T) {
	body, ctype := wrapInjectionEnvelope("codex", "Stop", "free-form text not a decision")
	if ctype[:16] != "application/json" {
		t.Errorf("Content-Type = %q, want application/json…", ctype)
	}
	// Must NOT be returned verbatim (it's not valid Codex stop shape).
	if body == "free-form text not a decision" {
		t.Error("non-decision Stop content should not pass through verbatim")
	}
	// Should be the additionalContext wrap as a fallback.
	var got struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.HookSpecificOutput.AdditionalContext != "free-form text not a decision" {
		t.Errorf("additionalContext = %q, want fallback wrap of original content", got.HookSpecificOutput.AdditionalContext)
	}
}
