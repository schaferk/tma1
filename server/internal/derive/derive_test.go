package derive

import "testing"

func TestFieldsFilePathExtraction(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
	}{
		{"cc-edit", `{"file_path":"server/handler.go","old_string":"foo","new_string":"bar"}`, "server/handler.go"},
		{"codex-apply-patch", `{"file_path":"src/main.rs","content":"..."}`, "src/main.rs"},
		{"truncated", `{"file_path":"deep/nested/`, ""}, // unterminated → no match
		{"no-file-path", `{"command":"ls -la"}`, ""},
		{"path-with-spaces", `{"file_path":"my dir/auth.go"}`, "my dir/auth.go"},
		{"path-with-dot", `{"file_path":"./relative/path.py"}`, "./relative/path.py"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fp, _, _, _ := Fields("PreToolUse", "Edit", c.input, "", "")
			if fp != c.want {
				t.Errorf("filePath = %q, want %q", fp, c.want)
			}
		})
	}
}

func TestFieldsCmdPrefixOnlyForShellTools(t *testing.T) {
	input := `{"command":"go test ./..."}`

	// Bash tool — should extract.
	_, cmd, _, _ := Fields("PreToolUse", "Bash", input, "", "")
	if cmd != "go test ./..." {
		t.Errorf("Bash: cmdPrefix = %q, want %q", cmd, "go test ./...")
	}

	// Codex exec_command — should extract (same conventions, different name).
	_, cmd, _, _ = Fields("PreToolUse", "exec_command", input, "", "")
	if cmd != "go test ./..." {
		t.Errorf("exec_command: cmdPrefix = %q, want %q", cmd, "go test ./...")
	}

	// Non-shell tool — should NOT extract (preserves field-shape contract).
	_, cmd, _, _ = Fields("PreToolUse", "Edit", input, "", "")
	if cmd != "" {
		t.Errorf("Edit: cmdPrefix = %q, want empty (Edit is not a shell tool)", cmd)
	}
}

func TestFieldsSuccessFromEventName(t *testing.T) {
	// PostToolUse → success = &true, no errSummary.
	_, _, success, errSummary := Fields("PostToolUse", "Bash", "", "ok", "")
	if success == nil || !*success {
		t.Error("PostToolUse should yield success = &true")
	}
	if errSummary != "" {
		t.Errorf("PostToolUse errSummary = %q, want empty", errSummary)
	}

	// PostToolUseFailure → success = &false, errSummary = toolResult.
	errText := "exit 1: build failed"
	_, _, success, errSummary = Fields("PostToolUseFailure", "Bash", "", errText, "")
	if success == nil || *success {
		t.Error("PostToolUseFailure should yield success = &false")
	}
	if errSummary != errText {
		t.Errorf("errSummary = %q, want %q", errSummary, errText)
	}

	// Failure with empty toolResult falls back to message.
	_, _, _, errSummary = Fields("PostToolUseFailure", "Bash", "", "", "fallback msg")
	if errSummary != "fallback msg" {
		t.Errorf("errSummary fallback = %q, want %q", errSummary, "fallback msg")
	}

	// Non-PostToolUse events: success = nil.
	for _, ev := range []string{"PreToolUse", "SessionStart", "UserPromptSubmit", "Stop", ""} {
		_, _, s, _ := Fields(ev, "Bash", "", "", "")
		if s != nil {
			t.Errorf("%q: success should be nil, got %v", ev, *s)
		}
	}
}

func TestFieldsCmdPrefixUnescape(t *testing.T) {
	// JSON-escape sequences in command should be unescaped.
	input := `{"command":"echo \"hello\nworld\""}`
	_, cmd, _, _ := Fields("PreToolUse", "Bash", input, "", "")
	want := "echo \"hello\nworld\""
	if cmd != want {
		t.Errorf("cmdPrefix = %q, want %q", cmd, want)
	}
}
