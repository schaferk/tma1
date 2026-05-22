package perception

import "testing"

func TestNormalizePeerAgent(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"all", ""},
		{"ALL", ""},
		{"*", ""},
		{"codex", "codex"},
		{"CODEX", "codex"},
		{"openclaw", "openclaw"},
		{"copilot", "copilot_cli"},       // alias
		{"Copilot", "copilot_cli"},       // case-insensitive alias
		{"copilot-cli", "copilot_cli"},   // hyphen alias
		{"github-copilot", "copilot_cli"},
		{"copilot_cli", "copilot_cli"},
		// CC aliases — the Codex skill table documents these as valid
		// inputs, so the server must accept them too. Without the
		// server-side fallback, a skill author drift (or a direct MCP
		// caller) typing "cc" hits "invalid agent_source 'cc'".
		{"cc", "claude_code"},
		{"CC", "claude_code"},
		{"claude", "claude_code"},
		{"Claude", "claude_code"},
		{"claude-code", "claude_code"},
		{"claudecode", "claude_code"},
		{"claude_code", "claude_code"},
		{"unknown-agent", "unknown-agent"}, // passes through; validator rejects later
	}
	for _, c := range cases {
		if got := normalizePeerAgent(c.in); got != c.want {
			t.Errorf("normalizePeerAgent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPeerCwdFilter(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		// Absolute path: anchored prefix, not basename. /foo must not match /foobar.
		{"/Users/dennis/tma1", "AND (cwd = '/Users/dennis/tma1' OR cwd LIKE '/Users/dennis/tma1/%') "},
		// Trailing slash normalized.
		{"/Users/dennis/tma1/", "AND (cwd = '/Users/dennis/tma1' OR cwd LIKE '/Users/dennis/tma1/%') "},
		// Bare name falls back to legacy basename LIKE.
		{"tma1", "AND cwd LIKE '%/tma1%' "},
		// SQL injection in the input gets escaped (single quote doubled).
		{"foo'bar", "AND cwd LIKE '%/foo''bar%' "},
		// LIKE wildcards in project name are neutralised via backslash
		// (GreptimeDB's only supported LIKE escape char).
		{"a%b_c", `AND cwd LIKE '%/a\%b\_c%' `},
		// '!' is no longer special — passes through literally.
		{"go!foo", "AND cwd LIKE '%/go!foo%' "},
		// Backslash in input gets escaped so it stays literal in the pattern.
		{`a\b`, `AND cwd LIKE '%/a\\b%' `},
	}
	for _, c := range cases {
		if got := peerCwdFilter(c.in); got != c.want {
			t.Errorf("peerCwdFilter(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidPeerAgentsCoversAllFourAdapters(t *testing.T) {
	// Each agent that can write to tma1_hook_events / tma1_messages
	// must be a queryable peer from every other agent's MCP tool.
	// Codex calling with agent_source="claude_code" was rejected by
	// an earlier draft that hard-coded CC as the caller; this test
	// guards against that regression.
	for _, want := range []string{"claude_code", "codex", "openclaw", "copilot_cli"} {
		if !validPeerAgents[want] {
			t.Errorf("expected %q to be a valid peer agent", want)
		}
	}
}

func TestPeerAgentListExcludesCaller(t *testing.T) {
	// The empty-agent_source fan-out must exclude the caller so an
	// agent invoking `/tma1-peer` doesn't see its own sessions
	// returned as "peers". With Caller empty (HTTP API path) all
	// four ship.
	cases := []struct {
		caller string
		want   []string
	}{
		{"claude_code", []string{"codex", "copilot_cli", "openclaw"}},
		{"codex", []string{"claude_code", "copilot_cli", "openclaw"}},
		{"openclaw", []string{"claude_code", "codex", "copilot_cli"}},
		{"copilot_cli", []string{"claude_code", "codex", "openclaw"}},
		{"", []string{"claude_code", "codex", "copilot_cli", "openclaw"}},
		{"unknown_agent", []string{"claude_code", "codex", "copilot_cli", "openclaw"}},
	}
	for _, c := range cases {
		b := &Bundler{Caller: c.caller}
		got := b.peerAgentList()
		if len(got) != len(c.want) {
			t.Errorf("caller=%q: len=%d, want %d (got=%v)", c.caller, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("caller=%q: got %v, want %v", c.caller, got, c.want)
				break
			}
		}
	}
}
