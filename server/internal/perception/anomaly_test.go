package perception

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFirstErrorLineFindsTheActualError(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"go compile",
			"go: building...\n# github.com/foo/bar\n./pkg/auth.go:42:7: undefined: bar\nmake: *** [build] Error 1",
			"./pkg/auth.go:42:7: undefined: bar"},
		{"rustc",
			"   Compiling foo v0.1.0\nerror[E0282]: type annotations needed\n  --> src/lib.rs:88:5",
			"error[E0282]: type annotations needed"},
		{"pytest assertion",
			"collected 1 item\ntest_x.py F\n=========\nFAILED test_x.py::test_y - AssertionError",
			"FAILED test_x.py::test_y - AssertionError"},
		{"node uncaught",
			"server starting...\nUncaught Exception: TypeError at handler.js:12",
			"Uncaught Exception: TypeError at handler.js:12"},
		{"no marker",
			"Compiling foo...\nDone.",
			""},
		{"empty",
			"",
			""},
		{"only whitespace",
			"   \n  \n",
			""},
	}
	for _, c := range cases {
		if got := firstErrorLine(c.raw); got != c.want {
			t.Errorf("%s: firstErrorLine = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExtractErrorLineCrossLanguage(t *testing.T) {
	// Each entry is one synthetic error blob the build/test runner of
	// some language might emit. We assert extractErrorLine pulls the
	// line number that points into `base`, and returns "" when the
	// number names a different file or is missing entirely.
	cases := []struct {
		name, result, base, want string
	}{
		{"go compiler", "./pkg/auth.go:42:7: undefined: bar", "auth.go", "42"},
		{"clang/gcc",   "src/util.c:128: error: expected ';'", "util.c", "128"},
		{"rustc",       "error[E0282]: type annotations needed\n  --> src/lib.rs:88:5", "lib.rs", "88"},
		{"tsc",         "src/index.ts:14:3 - error TS2322: Type 'string' is not assignable", "index.ts", "14"},
		{"python tb",   "Traceback ...\n  File \"app/handlers.py\", line 27, in handle\n    raise ValueError", "handlers.py", "27"},
		{"unrelated",   "./pkg/other.go:10: missing return", "auth.go", ""},
		{"no line",     "linker: undefined symbol _foo in auth.go", "auth.go", ""},
		{"empty",       "", "auth.go", ""},
		{"empty base",  "auth.go:1: blah", "", ""},
	}
	for _, c := range cases {
		got := extractErrorLine(c.result, c.base)
		if got != c.want {
			t.Errorf("%s: extractErrorLine = %q, want %q\nresult=%q base=%q",
				c.name, got, c.want, c.result, c.base)
		}
	}
}

func TestTestRunnerPrefixesAcrossLanguages(t *testing.T) {
	// Locks the cross-language coverage of R-test-stuck. The rule's
	// SQL builds LIKE clauses from testRunnerPrefixes; this test
	// asserts the prefix set keeps covering the languages we care
	// about. The matching semantics mirror the SQL LIKE 'prefix%' --
	// case-sensitive prefix match -- so the test lowercases inputs
	// because tool_command_prefix arrives lowercased at ingest.
	matches := func(cmd string) bool {
		cmd = strings.TrimSpace(strings.ToLower(cmd))
		if cmd == "" {
			return false
		}
		for _, p := range testRunnerPrefixes {
			if strings.HasPrefix(cmd, p) {
				rest := cmd[len(p):]
				if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
					return true
				}
			}
		}
		return false
	}
	positive := []string{
		"go test ./...",
		"GO TEST ./pkg", // case-insensitive
		"cargo test --workspace",
		"cargo nextest run",
		"pytest -k foo",
		"py.test tests/",
		"python -m pytest",
		"python -m unittest discover",
		"npm test -- --watch",
		"npm run test",
		"yarn test --ci",
		"pnpm test",
		"pnpm run test",
		"jest --bail",
		"npx jest --watchAll=false",
		"vitest run",
		"npx vitest",
		"mocha --recursive",
		"npx mocha test/",
		"phpunit --testdox",
		"vendor/bin/phpunit",
		"rspec spec/",
		"bundle exec rspec --format doc",
		"mix test --trace",
	}
	for _, cmd := range positive {
		if !matches(cmd) {
			t.Errorf("expected test-runner match: %q", cmd)
		}
	}

	negative := []string{
		"",
		"   ",
		"go build",
		"go testify", // word-boundary guard: not "go test"
		"cargo build",
		"npm install",
		"npm run dev",
		"make test", // intentionally NOT covered -- Makefile target names are project-defined
		"./bin/foo",
		"docker compose up",
	}
	for _, cmd := range negative {
		if matches(cmd) {
			t.Errorf("expected NO match: %q", cmd)
		}
	}
}

func TestAnomalyCacheHitAndExpiry(t *testing.T) {
	c := newAnomalyCache(50 * time.Millisecond)
	want := []Anomaly{{Kind: "file_loop_edit", Severity: SeverityHigh}}

	c.set("s1", want)

	got, ok := c.get("s1")
	if !ok || len(got) != 1 || got[0].Kind != "file_loop_edit" {
		t.Fatalf("expected cache hit, got ok=%v got=%+v", ok, got)
	}

	time.Sleep(80 * time.Millisecond)

	if _, ok := c.get("s1"); ok {
		t.Errorf("entry should have expired")
	}
}

func TestAnomalyCacheInvalidate(t *testing.T) {
	c := newAnomalyCache(time.Hour)
	c.set("s1", []Anomaly{{Kind: "x"}})
	c.invalidate("s1")
	if _, ok := c.get("s1"); ok {
		t.Error("invalidate did not drop entry")
	}
}

func TestAnomalyCacheHistoryEvictsStaleSessions(t *testing.T) {
	// Verifies the history map drops sessions whose newest emit is
	// older than historyMaxAge, while keeping fresh ones. The
	// suppression path runs the eviction inline so a long-running
	// server can't accumulate dead session state.
	c := newAnomalyCache(time.Hour)

	// Seed historyGCMinSize+1 sessions with stale emits so the GC
	// threshold is crossed and the scan actually runs.
	staleTs := time.Now().Add(-2 * historyMaxAge)
	for i := 0; i < historyGCMinSize+1; i++ {
		sid := "stale-" + strconv.Itoa(i)
		c.history[sid] = map[string]emitState{
			"k|high|": {LastEmittedAt: staleTs},
		}
	}
	// One fresh session that must survive.
	c.history["fresh"] = map[string]emitState{
		"k|high|": {LastEmittedAt: time.Now()},
	}

	// Trigger eviction via the normal suppression path.
	c.suppressWithResolution("trigger", []Anomaly{{Kind: "k", Severity: "high"}}, nil)

	if _, ok := c.history["fresh"]; !ok {
		t.Error("fresh session was evicted")
	}
	if _, ok := c.history["stale-0"]; ok {
		t.Error("stale session was retained")
	}
	if len(c.history) > 2 { // "fresh" + "trigger"
		t.Errorf("expected ≤ 2 surviving sessions, got %d", len(c.history))
	}
}

func TestAnomalyStableKeyDistinctByKindAndFiles(t *testing.T) {
	a := Anomaly{Kind: "x", Severity: "high", RelatedFiles: []string{"b", "a"}}
	b := Anomaly{Kind: "x", Severity: "high", RelatedFiles: []string{"a", "b"}}
	if a.stableKey() != b.stableKey() {
		t.Errorf("stableKey should be order-insensitive on RelatedFiles; got %q vs %q", a.stableKey(), b.stableKey())
	}
	c := Anomaly{Kind: "x", Severity: "medium", RelatedFiles: []string{"a", "b"}}
	if a.stableKey() == c.stableKey() {
		t.Errorf("stableKey should differ when severity differs")
	}
}

func TestAnomalyCacheSuppressionDropsRepeats(t *testing.T) {
	c := newAnomalyCache(time.Minute)
	a := Anomaly{Kind: "k1", Severity: SeverityMedium, Channel: ChannelUserPromptSubmit}
	first := c.suppress("s1", []Anomaly{a})
	if len(first) != 1 {
		t.Fatalf("first emit should pass through; got %d", len(first))
	}
	second := c.suppress("s1", []Anomaly{a})
	if len(second) != 0 {
		t.Errorf("repeat within suppression window should be silent; got %d", len(second))
	}
}

func TestAnomalyCacheSuppressionPerSession(t *testing.T) {
	c := newAnomalyCache(time.Minute)
	a := Anomaly{Kind: "k1", Severity: SeverityMedium}
	_ = c.suppress("s1", []Anomaly{a}) // emit once for s1
	// Same key on a different session must still emit.
	if got := c.suppress("s2", []Anomaly{a}); len(got) != 1 {
		t.Errorf("suppression must be per-session, got %d for s2", len(got))
	}
}

func TestAnomalyCacheResolutionResetsAndReemits(t *testing.T) {
	c := newAnomalyCache(time.Minute)
	a := Anomaly{Kind: "stale_file_view", Severity: SeverityHigh, RelatedFiles: []string{"src/foo.go"}}

	// First emit lands.
	first := c.suppressWithResolution("s1", []Anomaly{a}, nil)
	if len(first) != 1 {
		t.Fatalf("first emit should pass through; got %d", len(first))
	}

	// Same key again within the silence window — silent.
	silent := c.suppressWithResolution("s1", []Anomaly{a}, nil)
	if len(silent) != 0 {
		t.Errorf("within suppression window should stay silent; got %d", len(silent))
	}

	// Now mark the key resolved. Even within the silence window the
	// anomaly should emit again, and FirstEmittedAt should reset.
	key := a.stableKey()
	resolved := map[string]bool{key: true}
	reemitted := c.suppressWithResolution("s1", []Anomaly{a}, resolved)
	if len(reemitted) != 1 {
		t.Fatalf("resolved key should re-emit; got %d", len(reemitted))
	}
	if !reemitted[0].FirstEmittedAt.After(first[0].FirstEmittedAt) {
		t.Errorf("resolved re-emit should refresh FirstEmittedAt; first=%v reemit=%v",
			first[0].FirstEmittedAt, reemitted[0].FirstEmittedAt)
	}
}

func TestAnomalyCacheResolutionMapIgnoredForUnseenKeys(t *testing.T) {
	c := newAnomalyCache(time.Minute)
	a := Anomaly{Kind: "x", Severity: SeverityMedium}
	// Mark resolved for a key we've never emitted. The candidate is
	// brand new so emits normally; the resolved flag has nothing to undo.
	resolved := map[string]bool{a.stableKey(): true}
	got := c.suppressWithResolution("s1", []Anomaly{a}, resolved)
	if len(got) != 1 {
		t.Errorf("fresh candidate should emit regardless of resolved map; got %d", len(got))
	}
}

func TestDetectStaleEdit(t *testing.T) {
	cases := []struct {
		name    string
		reads   []int64
		edits   []int64
		changes []int64
		want    int64
	}{
		{
			name: "plan scenario: read T1, change T2, edit T3 with no re-read",
			// T1=100, T2=150, T3=200. Edit at T3 is based on the T1 read,
			// which predates the T2 change. Rule fires; suggestion uses T2.
			reads:   []int64{100},
			edits:   []int64{200},
			changes: []int64{150},
			want:    150,
		},
		{
			name: "agent re-read after change — no stale view",
			// Read T1=100, change T2=150, re-read T3=170, edit T4=200.
			// Latest read before edit is T3 > T2, so view is fresh.
			reads:   []int64{100, 170},
			edits:   []int64{200},
			changes: []int64{150},
			want:    0,
		},
		{
			name: "edit without ever reading — rule doesn't apply",
			// Agents that Edit a never-Read file aren't in the stale-view
			// scenario (they may be creating it). Skip.
			reads:   nil,
			edits:   []int64{200},
			changes: []int64{150},
			want:    0,
		},
		{
			name: "change before agent ever read it — agent's view is current",
			// Change at T0=50, read at T1=100, edit at T2=200. The read
			// already captured the post-change content. No staleness.
			reads:   []int64{100},
			edits:   []int64{200},
			changes: []int64{50},
			want:    0,
		},
		{
			name: "multiple stale edits — return latest triggering change",
			// Read T=100, change T=150, edit T=200, change T=250, edit T=300.
			// Both edits triggered; suggestion should reference the latest
			// change (T=250) which is the most useful staleness clue.
			reads:   []int64{100},
			edits:   []int64{200, 300},
			changes: []int64{150, 250},
			want:    250,
		},
		{
			name:    "no external changes — empty result",
			reads:   []int64{100},
			edits:   []int64{200},
			changes: nil,
			want:    0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectStaleEdit(c.reads, c.edits, c.changes)
			if got != c.want {
				t.Errorf("detectStaleEdit(reads=%v, edits=%v, changes=%v) = %d, want %d",
					c.reads, c.edits, c.changes, got, c.want)
			}
		})
	}
}

func TestAnomalyCacheGCDropsOtherExpiredEntries(t *testing.T) {
	c := newAnomalyCache(10 * time.Millisecond)
	c.set("expired", []Anomaly{{Kind: "x"}})
	time.Sleep(20 * time.Millisecond)
	// set() for a different key triggers opportunistic GC of expired peers.
	c.set("fresh", []Anomaly{{Kind: "y"}})
	c.mu.Lock()
	_, has := c.items["expired"]
	c.mu.Unlock()
	if has {
		t.Error("opportunistic GC did not drop expired peer")
	}
}
