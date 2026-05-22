package transcript

import (
	"bufio"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestCodexParserAgainstRealFile drives processCodexLine against the
// real Codex rollout JSONL files in ~/.codex/sessions/ if they exist,
// captures every SQL the parser emits, and asserts that no insert
// uses a filename-prefix pseudo session_id (i.e. nothing like
// "rollout-2026-05-22T12-01-53"). SKIPPED when the files aren't
// present (CI environment) so this stays a developer-machine smoke,
// not a fragile CI test.
//
// The two files this targets are the exact reproduction from the
// dogfood report: one main `cli` session and one orphan
// `subagent:review` session whose session_meta has no parent_id
// field. Both must produce UUID-shaped session_ids in the inserts.
func TestCodexParserAgainstRealFile(t *testing.T) {
	candidates := []string{
		os.ExpandEnv("$HOME/.codex/sessions/2026/05/22/rollout-2026-05-22T11-57-38-019e510c-dfff-7b43-9c58-067522717c80.jsonl"),
		os.ExpandEnv("$HOME/.codex/sessions/2026/05/22/rollout-2026-05-22T12-01-53-019e5110-c0d9-7653-8bb0-ced77e673716.jsonl"),
	}
	var present []string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		t.Skip("no real Codex rollout files present; this smoke needs dogfood state")
	}

	sqlCh := make(chan string, 4096)
	ts := httptest.NewServer(httpTestHandler(sqlCh))
	defer ts.Close()

	oldClient := httpClient
	httpClient = ts.Client()
	defer func() { httpClient = oldClient }()

	w := &Watcher{
		sqlURL: ts.URL,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	for _, path := range present {
		t.Logf("processing %s", filepath.Base(path))

		// Pre-scan exactly like the scanner does.
		if uuid, isMain := peekCodexMainUUID(path); isMain && uuid != "" {
			base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			prefix := codexSessionGroup(base)
			w.recordCodexParentSession(prefix, uuid)
			t.Logf("  pre-scan: main session, recorded %s → %s", prefix, uuid)
		} else {
			t.Logf("  pre-scan: not a main session (orphan subagent or empty)")
		}

		// Feed every line through processCodexLine, exactly like
		// tailCodexFile would on tma1-server startup.
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		fctx := &codexFileContext{fileID: "codex:" + base}
		sessionID := codexSessionGroup(base)

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1<<16), 8<<20)
		seen := make(map[string]struct{})
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			w.processCodexLine(sessionID, line, seen, fctx)
		}
		_ = f.Close()

		t.Logf("  fctx.sessionID after: %q   conversationID: %q",
			fctx.sessionID, fctx.conversationID)
	}

	// Drain captured SQL. Background goroutines block on insertSem
	// and write synchronously to the httptest handler; 300 ms grace
	// is plenty for stragglers.
	idRe := regexp.MustCompile(`VALUES \(\d+, '([^']+)'`)
	allInserts := map[string]map[string]int{} // session_id → event_type → count

	deadline := time.Now().Add(500 * time.Millisecond)
drain:
	for {
		select {
		case sql := <-sqlCh:
			m := idRe.FindStringSubmatch(sql)
			if m == nil {
				continue
			}
			sid := m[1]
			evt := extractInsertEventType(sql)
			if allInserts[sid] == nil {
				allInserts[sid] = map[string]int{}
			}
			allInserts[sid][evt]++
		default:
			if time.Now().After(deadline) {
				break drain
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	t.Logf("--- session_id distribution across all parsed lines ---")
	rolloutSeen := false
	for sid, evts := range allInserts {
		t.Logf("  %s", sid)
		for evt, n := range evts {
			t.Logf("    %s × %d", evt, n)
		}
		if strings.HasPrefix(sid, "rollout-") {
			rolloutSeen = true
		}
	}

	if rolloutSeen {
		t.Errorf("at least one insert still used a filename-prefix session_id (rollout-...); post-fix path should always resolve to a UUID")
	}
	if len(allInserts) == 0 {
		t.Error("no SQL captured — processCodexLine never wrote anything")
	}
}

func extractInsertEventType(sql string) string {
	re := regexp.MustCompile(`'(SessionStart|SubagentStart|SubagentStop|PreToolUse|PostToolUse|UserPromptSubmit|Stop)'`)
	m := re.FindStringSubmatch(sql)
	if m == nil {
		if strings.Contains(sql, "tma1_messages") {
			return "message"
		}
		return "?"
	}
	return m[1]
}
