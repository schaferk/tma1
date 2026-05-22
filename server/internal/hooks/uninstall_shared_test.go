package hooks

import (
	"errors"
	"strings"
	"testing"
)

func TestRemoveInstructionsBlockBothMarkersPresent(t *testing.T) {
	existing := []byte("# Project doc\n\nUser content here.\n\n<!-- tma1:start -->\n## TMA1 Context Layer\nblah blah\n<!-- tma1:end -->\n\nMore user content.\n")
	out, removed, err := removeInstructionsBlock(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true")
	}
	got := string(out)
	if strings.Contains(got, "<!-- tma1:start -->") || strings.Contains(got, "<!-- tma1:end -->") {
		t.Errorf("markers still present: %q", got)
	}
	if !strings.Contains(got, "# Project doc") || !strings.Contains(got, "More user content") {
		t.Errorf("user content lost: %q", got)
	}
	// Should not leave a multi-line blank scar.
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("blank-line scar present: %q", got)
	}
}

func TestRemoveInstructionsBlockNoMarkers(t *testing.T) {
	existing := []byte("# Project doc\n\nJust user content, never installed.\n")
	out, removed, err := removeInstructionsBlock(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed {
		t.Error("expected removed=false on no-markers content")
	}
	if string(out) != string(existing) {
		t.Errorf("content changed: %q", out)
	}
}

func TestRemoveInstructionsBlockHalfStateStartOnly(t *testing.T) {
	// User deleted the end marker and possibly kept legitimate content
	// after the start. Refuse to guess where our content ends.
	existing := []byte("# Project doc\n\n<!-- tma1:start -->\n## TMA1\nstuff\nUser put their own content here after deleting the end marker.\n")
	out, removed, err := removeInstructionsBlock(existing)
	if !errors.Is(err, ErrInstructionsHalfState) {
		t.Fatalf("expected ErrInstructionsHalfState, got %v", err)
	}
	if removed {
		t.Error("expected removed=false in half-state")
	}
	if string(out) != string(existing) {
		t.Errorf("content modified despite half-state: %q", out)
	}
}

func TestRemoveInstructionsBlockHalfStateEndOnly(t *testing.T) {
	existing := []byte("# Doc\n<!-- tma1:end -->\nstray end marker\n")
	_, removed, err := removeInstructionsBlock(existing)
	if !errors.Is(err, ErrInstructionsHalfState) {
		t.Fatalf("expected ErrInstructionsHalfState, got %v", err)
	}
	if removed {
		t.Error("expected removed=false")
	}
}

func TestRemoveInstructionsBlockEndBeforeStart(t *testing.T) {
	// Pathological: end marker appears before start. Treat as half-state.
	existing := []byte("<!-- tma1:end -->\nbroken\n<!-- tma1:start -->\n")
	_, removed, err := removeInstructionsBlock(existing)
	if !errors.Is(err, ErrInstructionsHalfState) {
		t.Fatalf("expected ErrInstructionsHalfState, got %v", err)
	}
	if removed {
		t.Error("expected removed=false on end-before-start")
	}
}

func TestUnregisterTMA1HooksRemovesByID(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"id": "tma1", "matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/anything"}}},
				map[string]any{"id": "user-hook", "matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/user/script.sh"}}},
			},
		},
	}
	removed := unregisterTMA1Hooks(settings, []string{"PreToolUse"}, "/anything")
	if removed != 1 {
		t.Fatalf("expected 1 removal, got %d", removed)
	}
	hooks := settings["hooks"].(map[string]any)
	list := hooks["PreToolUse"].([]any)
	if len(list) != 1 {
		t.Fatalf("expected 1 entry left, got %d", len(list))
	}
	if list[0].(map[string]any)["id"] != "user-hook" {
		t.Errorf("wrong entry survived: %v", list[0])
	}
}

func TestUnregisterTMA1HooksRemovesByCommandPath(t *testing.T) {
	// Legacy entry: no `id` field, but command matches our hook script.
	// This is the path Codex's review explicitly called out.
	settings := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/home/u/.tma1/hooks/tma1-hook.sh"}}},
				map[string]any{"id": "user", "matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/other"}}},
			},
		},
	}
	removed := unregisterTMA1Hooks(settings, []string{"UserPromptSubmit"}, "/home/u/.tma1/hooks/tma1-hook.sh")
	if removed != 1 {
		t.Fatalf("expected 1 removal, got %d", removed)
	}
	list := settings["hooks"].(map[string]any)["UserPromptSubmit"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != "user" {
		t.Errorf("legacy unmarked tma1 entry not removed: %v", list)
	}
}

func TestUnregisterTMA1HooksNoMatchReturnsZero(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{"id": "user", "matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/user.sh"}}},
			},
		},
	}
	removed := unregisterTMA1Hooks(settings, []string{"PreToolUse"}, "/our.sh")
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

// TestRemoveInstructionsBlockIgnoresInProseMarker is the regression guard
// for the 2026-05-22 AGENTS.md damage. A developer wrote "...removes the
// <!-- tma1:start --> block" inside a documentation comment, then ran
// install. installInstructions matched the first marker — inside the
// prose — and erased everything down to the real <!-- tma1:end --> at
// EOF. Both install and uninstall must now match ONLY standalone-line
// markers; this test pins the uninstall side.
func TestRemoveInstructionsBlockIgnoresInProseMarker(t *testing.T) {
	existing := []byte("# Doc\n\n" +
		"```\n" +
		"# Removes the <!-- tma1:start --> block once installed.\n" +
		"```\n\n" +
		"User text mentioning <!-- tma1:end --> in a sentence.\n\n" +
		"<!-- tma1:start -->\n" +
		"## TMA1 Context Layer\n" +
		"real block content\n" +
		"<!-- tma1:end -->\n\n" +
		"Trailing user content.\n")

	out, removed, err := removeInstructionsBlock(existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true on real marker block")
	}
	got := string(out)
	// The prose mentions of the marker text must survive untouched.
	if !strings.Contains(got, "Removes the <!-- tma1:start --> block") {
		t.Error("prose mention of start marker was clobbered")
	}
	if !strings.Contains(got, "mentioning <!-- tma1:end --> in a sentence") {
		t.Error("prose mention of end marker was clobbered")
	}
	// The real block (and only the real block) must be gone.
	if strings.Contains(got, "real block content") {
		t.Errorf("real block content survived: %q", got)
	}
	if !strings.Contains(got, "Trailing user content") {
		t.Error("content after the real marker was destroyed")
	}
}

// TestIndexOfStandaloneLineSemantics directly exercises the helper that
// drives the fix. Covers leading whitespace, trailing whitespace, and
// the in-prose negative cases.
func TestIndexOfStandaloneLineSemantics(t *testing.T) {
	marker := "<!-- tma1:start -->"
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"bare line", marker, 0},
		{"trailing newline", marker + "\n", 0},
		{"leading newline", "\n" + marker, 1},
		{"surrounded blank lines", "alpha\n\n" + marker + "\n\nbeta", len("alpha\n\n")},
		{"only whitespace prefix", "  \t" + marker + "\nbody", len("  \t")},
		{"in-prose mention", "doc text <!-- tma1:start --> in line\n", -1},
		{"in-comment mention", "# explain <!-- tma1:start -->\n", -1},
		{"prose THEN standalone", "doc <!-- tma1:start --> ref\nbody\n<!-- tma1:start -->\nblock", len("doc <!-- tma1:start --> ref\nbody\n")},
		{"absent", "no marker here at all", -1},
	}
	for _, c := range cases {
		got := indexOfStandaloneLine([]byte(c.input), marker)
		if got != c.want {
			t.Errorf("%s: got %d, want %d (input=%q)", c.name, got, c.want, c.input)
		}
	}
}

func TestRemoveMCPServerEntry(t *testing.T) {
	servers := map[string]any{"tma1": map[string]any{}, "atlassian": map[string]any{}}
	if !removeMCPServerEntry(servers, "tma1") {
		t.Error("expected true on existing key")
	}
	if _, ok := servers["tma1"]; ok {
		t.Error("tma1 not removed")
	}
	if _, ok := servers["atlassian"]; !ok {
		t.Error("atlassian incorrectly removed")
	}
	if removeMCPServerEntry(servers, "tma1") {
		t.Error("expected false on second call")
	}
	if removeMCPServerEntry(nil, "tma1") {
		t.Error("expected false on nil map")
	}
}
