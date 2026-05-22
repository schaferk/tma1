package hooks

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installThenUninstall is the common harness: run a fresh install in a
// sandbox HOME, then run uninstall. Returns the home, project dir, and
// the uninstall report.
func installThenUninstallCC(t *testing.T, purgeData bool) (home, project string, rep UninstallReport) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	project = filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	inst := &ClaudeCodeInstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		Port:       14318,
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	if _, err := inst.Install(); err != nil {
		t.Fatalf("install: %v", err)
	}
	unin := &ClaudeCodeUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
		PurgeData:  purgeData,
	}
	r, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	return home, project, r
}

func TestUninstallEndToEndAfterInstall(t *testing.T) {
	home, project, _ := installThenUninstallCC(t, false)

	// settings.json: tma1 hook entries removed
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
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

	// claude.json: mcpServers.tma1 gone
	raw, err = os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("read claude.json: %v", err)
	}
	var cm map[string]any
	if err := json.Unmarshal(raw, &cm); err != nil {
		t.Fatalf("parse claude.json: %v", err)
	}
	servers, _ := cm["mcpServers"].(map[string]any)
	if _, ok := servers["tma1"]; ok {
		t.Error("mcpServers.tma1 still present after uninstall")
	}

	// CLAUDE.md: marker block gone, file may remain
	claude := filepath.Join(project, "CLAUDE.md")
	if data, err := os.ReadFile(claude); err == nil {
		if strings.Contains(string(data), "<!-- tma1:start -->") {
			t.Error("CLAUDE.md still has tma1 markers")
		}
	}

	// .gitignore: '.tma1-context.md' left in place (deliberate)
	if data, err := os.ReadFile(filepath.Join(project, ".gitignore")); err == nil {
		if !strings.Contains(string(data), ".tma1-context.md") {
			t.Error(".gitignore '.tma1-context.md' was deleted; uninstall should leave it in place")
		}
	}

	// Skills tree: tma1-* gone
	if entries, err := os.ReadDir(filepath.Join(home, ".claude", "skills")); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "tma1-") || e.Name() == "tma1" {
				t.Errorf("tma1 skill survived: %s", e.Name())
			}
		}
	}

	// Hook script gone
	if _, err := os.Stat(filepath.Join(home, ".tma1", "hooks", "tma1-hook.sh")); !os.IsNotExist(err) {
		t.Error("hook script still present after uninstall")
	}
}

func TestUninstallRemovesLegacyHookEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".tma1")

	// Seed a settings.json that has an entry WITHOUT id="tma1" but
	// whose command points at our hook script. Mirrors a pre-v2 install.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath := HookScriptPathFor(AdapterClaudeCode, dataDir)
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				// Legacy tma1 entry: no id, just command path.
				map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": hookPath}}},
				// User entry.
				map[string]any{"id": "user", "matcher": "", "hooks": []any{map[string]any{"type": "command", "command": "/user.sh"}}},
			},
		},
	}
	raw, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(settingsPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	unin := &ClaudeCodeUninstaller{DataDir: dataDir, Logger: slog.Default()}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	raw, _ = os.ReadFile(settingsPath)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	list := got["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != "user" {
		t.Errorf("legacy tma1 entry not removed: %v", list)
	}
	if len(rep.Removed) == 0 {
		t.Error("Removed report should mention legacy entry")
	}
}

func TestUninstallPreservesUserHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".tma1")
	inst := &ClaudeCodeInstaller{DataDir: dataDir, Port: 14318, Logger: slog.Default()}
	if _, err := inst.Install(); err != nil {
		t.Fatal(err)
	}
	// Inject a user-owned PreToolUse hook AFTER install.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	raw, _ := os.ReadFile(settingsPath)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	hooks := m["hooks"].(map[string]any)
	pt, _ := hooks["PreToolUse"].([]any)
	pt = append(pt, map[string]any{
		"id":      "user-graphify",
		"matcher": "",
		"hooks":   []any{map[string]any{"type": "command", "command": "/user/graphify.sh"}},
	})
	hooks["PreToolUse"] = pt
	raw, _ = json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(settingsPath, raw, 0o644)

	unin := &ClaudeCodeUninstaller{DataDir: dataDir, Logger: slog.Default()}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	raw, _ = os.ReadFile(settingsPath)
	_ = json.Unmarshal(raw, &m)
	pt = m["hooks"].(map[string]any)["PreToolUse"].([]any)
	var sawUser bool
	for _, e := range pt {
		em, _ := e.(map[string]any)
		if em["id"] == "user-graphify" {
			sawUser = true
		}
		if em["id"] == "tma1" {
			t.Error("tma1 entry should be gone")
		}
	}
	if !sawUser {
		t.Error("user-graphify entry was removed; should be preserved")
	}
}

func TestUninstallPreservesOtherMCPServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".tma1")
	inst := &ClaudeCodeInstaller{DataDir: dataDir, Port: 14318, Logger: slog.Default()}
	if _, err := inst.Install(); err != nil {
		t.Fatal(err)
	}
	// Inject a user MCP server.
	cfgPath := filepath.Join(home, ".claude.json")
	raw, _ := os.ReadFile(cfgPath)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	servers := m["mcpServers"].(map[string]any)
	servers["atlassian"] = map[string]any{"type": "stdio", "command": "/usr/bin/atlassian"}
	raw, _ = json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(cfgPath, raw, 0o644)

	unin := &ClaudeCodeUninstaller{DataDir: dataDir, Logger: slog.Default()}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	raw, _ = os.ReadFile(cfgPath)
	_ = json.Unmarshal(raw, &m)
	servers = m["mcpServers"].(map[string]any)
	if _, ok := servers["tma1"]; ok {
		t.Error("tma1 still present")
	}
	if _, ok := servers["atlassian"]; !ok {
		t.Error("atlassian was removed; uninstall should only touch tma1")
	}
}

func TestUninstallPreservesUserSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".tma1")
	inst := &ClaudeCodeInstaller{DataDir: dataDir, Port: 14318, Logger: slog.Default()}
	if _, err := inst.Install(); err != nil {
		t.Fatal(err)
	}
	// Seed user skill alongside.
	userSkill := filepath.Join(home, ".claude", "skills", "humanizer", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(userSkill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userSkill, []byte("# Humanizer\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	unin := &ClaudeCodeUninstaller{DataDir: dataDir, Logger: slog.Default()}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(userSkill); err != nil {
		t.Errorf("user skill destroyed by uninstall: %v", err)
	}
	// Tma1 skills should be gone.
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "tma1-peer", "SKILL.md")); !os.IsNotExist(err) {
		t.Error("tma1-peer skill survived uninstall")
	}
}

func TestUninstallIdempotent(t *testing.T) {
	home, project, _ := installThenUninstallCC(t, false)
	// Second pass: nothing to remove.
	unin := &ClaudeCodeUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	// Removed list should be empty (except possibly .gitignore Skipped row).
	for _, r := range rep.Removed {
		// The hook script + settings + mcp + instructions + skills/commands
		// should all be Skipped on the second pass. Removed should be 0.
		t.Errorf("second uninstall reported Removed entry: %s", r)
	}
}

func TestUninstallMissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	unin := &ClaudeCodeUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("uninstall on fresh HOME: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Errorf("unexpected Errors on fresh HOME: %v", rep.Errors)
	}
	if len(rep.Removed) > 0 {
		t.Errorf("unexpected Removed on fresh HOME: %v", rep.Removed)
	}
}

func TestUninstallRefusesMalformedSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("{ this is not json")
	if err := os.WriteFile(settingsPath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	unin := &ClaudeCodeUninstaller{DataDir: filepath.Join(home, ".tma1"), Logger: slog.Default()}
	if _, err := unin.Uninstall(); err == nil {
		t.Error("expected error on malformed settings.json")
	}
	// File must NOT have been overwritten.
	got, _ := os.ReadFile(settingsPath)
	if string(got) != string(original) {
		t.Errorf("malformed file modified: %q", got)
	}
}

func TestUninstallInstructionsHalfStateRefuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed CLAUDE.md with only the start marker.
	claudePath := filepath.Join(project, "CLAUDE.md")
	original := []byte("# Doc\n<!-- tma1:start -->\nstuff\nMore content user wrote.\n")
	if err := os.WriteFile(claudePath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	unin := &ClaudeCodeUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rep.HasErrors() {
		t.Error("expected HasErrors() true on half-marker file")
	}
	// File must be unchanged.
	got, _ := os.ReadFile(claudePath)
	if string(got) != string(original) {
		t.Errorf("half-state file modified: %q", got)
	}
}

func TestUninstallInstructionsScansBothFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed AGENTS.md with our block (simulating an AGENTS.md-only project
	// that received the CC install via the chooseInstructionsFile fallback).
	agents := filepath.Join(project, "AGENTS.md")
	content := []byte("# Project\n\n<!-- tma1:start -->\n## TMA1\nstuff\n<!-- tma1:end -->\n")
	if err := os.WriteFile(agents, content, 0o644); err != nil {
		t.Fatal(err)
	}
	unin := &ClaudeCodeUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, _ := os.ReadFile(agents)
	if strings.Contains(string(got), "<!-- tma1:start -->") {
		t.Error("CC adapter failed to remove block from AGENTS.md")
	}
	var sawAgents bool
	for _, p := range rep.InstructionsPaths {
		if p == agents {
			sawAgents = true
		}
	}
	if !sawAgents {
		t.Errorf("InstructionsPaths missing AGENTS.md: %v", rep.InstructionsPaths)
	}
}

// TestUninstallCCDualAdapterLeavesCodexBlockAlone is the regression
// guard for Codex's [P2] finding: in a project with both CC and Codex
// installed, each adapter owns a different instructions file (CC →
// CLAUDE.md, Codex → AGENTS.md). The previous uninstall_cc.go loop
// scanned BOTH files unconditionally and stripped the Codex block on
// the side. Now it must touch only its own file.
func TestUninstallCCDualAdapterLeavesCodexBlockAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "dual")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate dual-adapter state: CLAUDE.md carries CC's block,
	// AGENTS.md carries Codex's block.
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

	unin := &ClaudeCodeUninstaller{
		DataDir:    filepath.Join(home, ".tma1"),
		ProjectDir: project,
		Logger:     slog.Default(),
	}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	// CC's CLAUDE.md block must be gone.
	gotClaude, _ := os.ReadFile(claudePath)
	if strings.Contains(string(gotClaude), "<!-- tma1:start -->") {
		t.Errorf("CC block not removed from CLAUDE.md:\n%s", gotClaude)
	}

	// Codex's AGENTS.md block must be UNTOUCHED.
	gotAgents, _ := os.ReadFile(agentsPath)
	if !strings.Contains(string(gotAgents), "## TMA1 Context Layer (Codex)") {
		t.Errorf("CC uninstall destroyed the Codex block in AGENTS.md:\n%s", gotAgents)
	}
}

// TestWriteFileAtomicPreservesSymlink covers Codex's [P2] symlink
// finding. The previous writeFileAtomic renamed a temp file onto the
// symlink path; POSIX rename(2) replaces the symlink with the new
// regular file, breaking layouts like CLAUDE.md → AGENTS.md (this
// repo's own setup). After the fix, atomic writes must follow the
// symlink and modify the resolved target, leaving the symlink intact.
func TestWriteFileAtomicPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "AGENTS.md")
	link := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", link); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}

	if err := writeFileAtomic(link, []byte("rewritten\n"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	// Symlink must still be a symlink.
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("CLAUDE.md is no longer a symlink (mode=%v) — atomic write replaced it", info.Mode())
	}
	// And reading either name must show the new content (i.e. the
	// real file was rewritten, not the symlink).
	got, _ := os.ReadFile(target)
	if string(got) != "rewritten\n" {
		t.Errorf("AGENTS.md content not updated through symlink: %q", got)
	}
	gotViaLink, _ := os.ReadFile(link)
	if string(gotViaLink) != "rewritten\n" {
		t.Errorf("reading via symlink: %q", gotViaLink)
	}
}

func TestUninstallGitignoreLeftInPlace(t *testing.T) {
	home, project, rep := installThenUninstallCC(t, false)
	_ = home
	data, err := os.ReadFile(filepath.Join(project, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(data), ".tma1-context.md") {
		t.Error("uninstall removed '.tma1-context.md' from .gitignore; should leave in place")
	}
	var saw bool
	for _, s := range rep.Skipped {
		if strings.Contains(s, ".gitignore") {
			saw = true
		}
	}
	if !saw {
		t.Errorf(".gitignore Skipped row missing: %v", rep.Skipped)
	}
}

func TestUninstallDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, ".tma1")
	inst := &ClaudeCodeInstaller{DataDir: dataDir, Port: 14318, ProjectDir: project, Logger: slog.Default()}
	if _, err := inst.Install(); err != nil {
		t.Fatal(err)
	}
	settingsBefore, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))

	unin := &ClaudeCodeUninstaller{
		DataDir:    dataDir,
		ProjectDir: project,
		Logger:     slog.Default(),
		DryRun:     true,
	}
	rep, err := unin.Uninstall()
	if err != nil {
		t.Fatalf("dry-run uninstall: %v", err)
	}
	if len(rep.Removed) == 0 {
		t.Error("dry-run should report would-be removals")
	}
	settingsAfter, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if string(settingsBefore) != string(settingsAfter) {
		t.Error("dry-run modified settings.json")
	}
	// Hook script still present.
	if _, err := os.Stat(filepath.Join(home, ".tma1", "hooks", "tma1-hook.sh")); err != nil {
		t.Error("dry-run removed hook script")
	}
}

func TestUninstallPurgeDataFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dataDir := filepath.Join(home, ".tma1")
	// Seed data + bin to simulate post-runtime state.
	if err := os.MkdirAll(filepath.Join(dataDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "data", "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --purge-data: data + bin survive.
	unin := &ClaudeCodeUninstaller{DataDir: dataDir, Logger: slog.Default()}
	if _, err := unin.Uninstall(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "data", "marker")); err != nil {
		t.Errorf("data dir destroyed without --purge-data: %v", err)
	}

	// With --purge-data: data + bin removed.
	uninPurge := &ClaudeCodeUninstaller{DataDir: dataDir, Logger: slog.Default(), PurgeData: true}
	if _, err := uninPurge.Uninstall(); err != nil {
		t.Fatalf("purge uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "data")); !os.IsNotExist(err) {
		t.Error("data dir not purged with --purge-data")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "bin")); !os.IsNotExist(err) {
		t.Error("bin dir not purged with --purge-data")
	}
}
