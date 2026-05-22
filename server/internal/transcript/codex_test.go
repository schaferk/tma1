package transcript

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCodexSessionGroup(t *testing.T) {
	tests := []struct {
		name     string
		baseName string
		want     string
	}{
		{
			name:     "standard rollout filename",
			baseName: "rollout-2026-03-27T18-10-59-019d2ec6-958f-7cde-b25c-acde48001122",
			want:     "rollout-2026-03-27T18-10-59",
		},
		{
			name:     "unexpected filename falls back to full name",
			baseName: "session-without-timestamp",
			want:     "session-without-timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexSessionGroup(tt.baseName); got != tt.want {
				t.Fatalf("codexSessionGroup(%q) = %q, want %q", tt.baseName, got, tt.want)
			}
		})
	}
}

func TestCodexSubagentID(t *testing.T) {
	if got := codexSubagentID("codex:rollout-2026-03-27T18-10-59-a", "review"); got != "codex:rollout-2026-03-27T18-10-59-a" {
		t.Fatalf("codexSubagentID should prefer per-file id, got %q", got)
	}
	if got := codexSubagentID("", "review"); got != "review" {
		t.Fatalf("codexSubagentID should fall back to agent type, got %q", got)
	}
}

func TestProcessCodexLineCarriesConversationIDIntoSubagentLifecycle(t *testing.T) {
	sqlCh := make(chan string, 4)
	ts := httptest.NewServer(httpTestHandler(sqlCh))
	defer ts.Close()

	oldClient := httpClient
	httpClient = ts.Client()
	defer func() { httpClient = oldClient }()

	w := &Watcher{
		sqlURL: ts.URL,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	seen := make(map[string]struct{})
	fctx := &codexFileContext{fileID: "codex:rollout-2026-03-27T18-10-59-sub"}

	w.processCodexLine("rollout-2026-03-27T18-10-59",
		`{"timestamp":"2026-03-27T18:10:59Z","type":"session_meta","payload":{"id":"conv-123","source":{"subagent":"review"},"cwd":"/tmp/project"}}`,
		seen, fctx)
	w.processCodexLine("rollout-2026-03-27T18-10-59",
		`{"timestamp":"2026-03-27T18:11:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		seen, fctx)

	sqls := []string{waitForSQL(t, sqlCh), waitForSQL(t, sqlCh)}
	var sawStart, sawStop bool
	for _, sql := range sqls {
		if !strings.Contains(sql, "conv-123") {
			t.Fatalf("expected insert to include conversation_id, got %s", sql)
		}
		if strings.Contains(sql, "SubagentStart") {
			sawStart = true
		}
		if strings.Contains(sql, "SubagentStop") {
			sawStop = true
		}
	}
	if !sawStart || !sawStop {
		t.Fatalf("expected both SubagentStart and SubagentStop inserts, got %q", sqls)
	}
}

// TestCodexLiveGateSkipsSubagentEvents pins down the rule that the
// live-hook gate must NOT suppress SubagentStart / SubagentStop —
// Codex never POSTs those via hooks, so the JSONL parser is the only
// writer. Gating them silently dropped hierarchy data for any session
// the hook adapter was active for.
func TestCodexLiveGateSkipsSubagentEvents(t *testing.T) {
	sqlCh := make(chan string, 4)
	ts := httptest.NewServer(httpTestHandler(sqlCh))
	defer ts.Close()

	oldClient := httpClient
	httpClient = ts.Client()
	defer func() { httpClient = oldClient }()

	w := &Watcher{
		sqlURL:        ts.URL,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		IsLiveSession: func(string) bool { return true }, // every session is "live"
	}
	seen := make(map[string]struct{})
	fctx := &codexFileContext{fileID: "codex:rollout-test-sub"}

	// session_meta with subagent → SubagentStart (must NOT be gated).
	w.processCodexLine("rollout-test",
		`{"timestamp":"2026-05-22T05:00:00Z","type":"session_meta","payload":{"id":"conv-x","source":{"subagent":"review"}}}`,
		seen, fctx)
	// task_complete on a subagent file → SubagentStop (must NOT be gated).
	w.processCodexLine("rollout-test",
		`{"timestamp":"2026-05-22T05:00:01Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		seen, fctx)

	sqls := []string{waitForSQL(t, sqlCh), waitForSQL(t, sqlCh)}
	var sawStart, sawStop bool
	for _, sql := range sqls {
		if strings.Contains(sql, "SubagentStart") {
			sawStart = true
		}
		if strings.Contains(sql, "SubagentStop") {
			sawStop = true
		}
	}
	if !sawStart || !sawStop {
		t.Fatalf("subagent lifecycle rows must survive live-hook gate, got SQLs %q", sqls)
	}
}

// TestCodexLiveGateSuppressesHookCoveredEvents confirms the gate still
// fires for events the hook adapter actually posts (PreToolUse /
// PostToolUse), so we don't double-write rows.
func TestCodexLiveGateSuppressesHookCoveredEvents(t *testing.T) {
	sqlCh := make(chan string, 4)
	ts := httptest.NewServer(httpTestHandler(sqlCh))
	defer ts.Close()

	oldClient := httpClient
	httpClient = ts.Client()
	defer func() { httpClient = oldClient }()

	w := &Watcher{
		sqlURL:        ts.URL,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		IsLiveSession: func(string) bool { return true },
	}
	seen := make(map[string]struct{})
	fctx := &codexFileContext{fileID: "codex:rollout-test", live: true}

	// function_call → PreToolUse (hook-covered, MUST be gated).
	w.processCodexLine("rollout-test",
		`{"timestamp":"2026-05-22T05:00:00Z","type":"response_item","payload":{"type":"function_call","name":"bash","call_id":"c1"}}`,
		seen, fctx)

	select {
	case sql := <-sqlCh:
		t.Fatalf("expected no insert for hook-covered PreToolUse when live, got %s", sql)
	case <-time.After(150 * time.Millisecond):
		// expected — gate suppressed the insert
	}
}

func httpTestHandler(sqlCh chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(500)
			return
		}
		sqlCh <- r.Form.Get("sql")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"output":[]}`))
	}
}

func waitForSQL(t *testing.T, sqlCh <-chan string) string {
	t.Helper()
	select {
	case sql := <-sqlCh:
		return sql
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SQL insert")
		return ""
	}
}
