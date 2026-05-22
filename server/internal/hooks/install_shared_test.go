package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

// TestChooseInstructionsFile pins down the asymmetric resolution:
//   - Codex must never fall back to CLAUDE.md (Codex doesn't read it).
//   - CC may fall back to AGENTS.md (recent CC versions read both).
//
// The reviewer-flagged regression was Codex install on a Claude-only
// project silently landing the block in CLAUDE.md where Codex would
// never see it.
func TestChooseInstructionsFile(t *testing.T) {
	tests := []struct {
		name      string
		files     []string // files to create in projectDir
		preferred string
		wantBase  string
	}{
		{
			name:      "preferred exists — use it",
			files:     []string{"AGENTS.md"},
			preferred: "AGENTS.md",
			wantBase:  "AGENTS.md",
		},
		{
			name:      "CC fallback to AGENTS.md when CLAUDE.md missing",
			files:     []string{"AGENTS.md"},
			preferred: "CLAUDE.md",
			wantBase:  "AGENTS.md",
		},
		{
			name:      "Codex does NOT fall back to CLAUDE.md",
			files:     []string{"CLAUDE.md"}, // Claude-only project
			preferred: "AGENTS.md",
			wantBase:  "AGENTS.md", // must create AGENTS.md, not write to CLAUDE.md
		},
		{
			name:      "empty project — create preferred",
			files:     nil,
			preferred: "AGENTS.md",
			wantBase:  "AGENTS.md",
		},
		{
			name:      "both files exist — use preferred",
			files:     []string{"CLAUDE.md", "AGENTS.md"},
			preferred: "CLAUDE.md",
			wantBase:  "CLAUDE.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("# existing\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := chooseInstructionsFile(dir, tt.preferred)
			if filepath.Base(got) != tt.wantBase {
				t.Fatalf("chooseInstructionsFile(%q) = %q, want basename %q",
					tt.preferred, got, tt.wantBase)
			}
		})
	}
}
