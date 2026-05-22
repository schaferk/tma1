package hooks

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func installThenUninstallCodex(t *testing.T) (home, project string, rep UninstallReport) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	project = filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	inst := &CodexInstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		Port:       14318,
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	if _, err := inst.Install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	unin := &CodexUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	r, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	return home, project, r
}

func TestCodexUninstallEndToEndAfterInstall(t *testing.T) {
	home, project, _ := installThenUninstallCodex(t)

	// hooks.json: tma1 entries gone
	if raw, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json")); err == nil {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		hookSection, _ := m["hooks"].(map[string]any)
		for event, entries := range hookSection {
			list, _ := entries.([]any)
			for _, e := range list {
				em, _ := e.(map[string]any)
				if em["id"] == "tma1" {
					t.Errorf("event %q still has tma1 entry: %v", event, em)
				}
			}
		}
	}

	// config.toml: mcp_servers.tma1 gone
	if raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
		var m map[string]any
		if err := toml.Unmarshal(raw, &m); err != nil {
			t.Fatalf("parse config.toml: %v", err)
		}
		servers, _ := m["mcp_servers"].(map[string]any)
		if _, ok := servers["tma1"]; ok {
			t.Error("mcp_servers.tma1 still present")
		}
	}

	// AGENTS.md: marker block gone
	if data, err := os.ReadFile(filepath.Join(project, "AGENTS.md")); err == nil {
		if strings.Contains(string(data), "<!-- tma1:start -->") {
			t.Error("AGENTS.md still has tma1 markers")
		}
	}

	// .gitignore: .tma1-context.md preserved (deliberate)
	if data, err := os.ReadFile(filepath.Join(project, ".gitignore")); err == nil {
		if !strings.Contains(string(data), ".tma1-context.md") {
			t.Error(".gitignore '.tma1-context.md' removed; uninstall should leave in place")
		}
	}

	// tma1-peer skill under ~/.agents/skills/ gone
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "tma1-peer", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("tma1-peer skill survived uninstall")
	}

	// Hook script gone
	if _, err := os.Stat(filepath.Join(home, ".tma1", "hooks", "tma1-hook-codex.sh")); !os.IsNotExist(err) {
		t.Error("codex hook script still present after uninstall")
	}
}

func TestCodexUninstallPreservesUserMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".tma1")
	inst := &CodexInstaller{DataDir: dataDir, Port: 14318, Logger: slog.Default()}
	if _, err := inst.Install(); err != nil {
		t.Fatal(err)
	}
	// Inject a user MCP server.
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	raw, _ := os.ReadFile(cfgPath)
	var m map[string]any
	_ = toml.Unmarshal(raw, &m)
	servers, _ := m["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["atlassian"] = map[string]any{"command": "/usr/bin/atlassian"}
	m["mcp_servers"] = servers
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(buf.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	unin := &CodexUninstaller{DataDir: dataDir, Logger: slog.Default()}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	raw, _ = os.ReadFile(cfgPath)
	m = nil
	_ = toml.Unmarshal(raw, &m)
	servers, _ = m["mcp_servers"].(map[string]any)
	if _, ok := servers["tma1"]; ok {
		t.Error("tma1 still present")
	}
	if _, ok := servers["atlassian"]; !ok {
		t.Error("atlassian removed; uninstall should only touch tma1")
	}
}

// TestCodexUninstallDualAdapterLeavesCCBlockAlone mirrors the CC-side
// regression: uninstalling Codex must not touch CLAUDE.md, which in a
// dual-adapter setup carries CC's block.
func TestCodexUninstallDualAdapterLeavesCCBlockAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "dual")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(project, "CLAUDE.md")
	agentsPath := filepath.Join(project, "AGENTS.md")
	const ccBlock = "# CC\n<!-- tma1:start -->\n## TMA1 Context Layer (CC)\nblock\n<!-- tma1:end -->\n"
	const codexBlock = "# Codex\n<!-- tma1:start -->\n## TMA1 Context Layer (Codex)\nblock\n<!-- tma1:end -->\n"
	if err := os.WriteFile(claudePath, []byte(ccBlock), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agentsPath, []byte(codexBlock), 0o644); err != nil {
		t.Fatal(err)
	}

	unin := &CodexUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	gotAgents, _ := os.ReadFile(agentsPath)
	if strings.Contains(string(gotAgents), "<!-- tma1:start -->") {
		t.Errorf("Codex block not removed from AGENTS.md:\n%s", gotAgents)
	}
	gotClaude, _ := os.ReadFile(claudePath)
	if !strings.Contains(string(gotClaude), "## TMA1 Context Layer (CC)") {
		t.Errorf("Codex uninstall destroyed the CC block in CLAUDE.md:\n%s", gotClaude)
	}
}

func TestCodexUninstallIdempotent(t *testing.T) {
	home, project, _ := installThenUninstallCodex(t)
	unin := &CodexUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	for _, r := range rep.Removed {
		t.Errorf("second uninstall reported Removed entry: %s", r)
	}
}

func TestCodexUninstallMissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unin := &CodexUninstaller{DataDir: filepath.Join(home, ".tma1"), Logger: slog.Default()}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("uninstall on fresh HOME: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Errorf("unexpected Errors: %v", rep.Errors)
	}
	if len(rep.Removed) > 0 {
		t.Errorf("unexpected Removed: %v", rep.Removed)
	}
}

func TestCodexUninstallRefusesMalformedTOML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("not = valid = toml = at all")
	if err := os.WriteFile(cfgPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	unin := &CodexUninstaller{DataDir: filepath.Join(home, ".tma1"), Logger: slog.Default()}
	if _, err := unin.Uninstall(); err == nil {
		t.Error("expected error on malformed config.toml")
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != string(original) {
		t.Error("malformed file modified despite refusing to overwrite")
	}
}
