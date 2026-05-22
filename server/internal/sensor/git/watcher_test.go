package git

import (
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestStaticShouldIgnorePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/repo/.git/HEAD", true},
		{"/repo/node_modules/foo/index.js", true},
		{"/repo/dist/bundle.js", true},
		{"/repo/build/x.o", true},
		{"/repo/bin/tma1-server", true},      // dogfood report: bin/ was leaking
		{"/repo/out/foo.class", true},
		{"/repo/vendor/x.go", true},
		{"/repo/.venv/bin/python", true},
		{"/repo/.tma1/state.json", true},     // tma1's own data dir
		{"/repo/__pycache__/x.cpython-311.pyc", true},
		{"/repo/x.pyc", true},
		{"/repo/.DS_Store", true},
		{"/repo/.claude/settings.local.json", true},
		{"/repo/.tma1-context.md", true},
		// Atomic-write tempfiles must be skipped — capturing them produces
		// a noisy "file_added" event per editor save with no signal value.
		{"/repo/main.go.tmp.27019.4ef42db74560", true},
		{"/repo/src/main.go", false},
		{"/repo/README.md", false},
		// Windows-style paths: fsnotify hands us backslashes on
		// Windows. Without ToSlash normalization these never matched
		// any POSIX fragment, so the recursive watcher descended into
		// .git/ and node_modules/.
		{`C:\repo\.git\HEAD`, true},
		{`C:\repo\node_modules\foo\index.js`, true},
		{`C:\repo\.tma1\state.json`, true},
		{`C:\repo\src\main.go`, false},
	}
	for _, tc := range cases {
		if got := staticShouldIgnorePath(tc.path); got != tc.want {
			t.Errorf("staticShouldIgnorePath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestClassifyFsOp(t *testing.T) {
	cases := []struct {
		op   fsnotify.Op
		want string
	}{
		{fsnotify.Create, ChangeTypeFileAdded},
		{fsnotify.Remove, ChangeTypeFileDeleted},
		{fsnotify.Rename, ChangeTypeFileDeleted},
		{fsnotify.Write, ChangeTypeFileModified},
		{fsnotify.Chmod, ChangeTypeFileModified}, // shouldn't be reached in practice
	}
	for _, tc := range cases {
		if got := classifyFsOp(tc.op); got != tc.want {
			t.Errorf("classifyFsOp(%v) = %q, want %q", tc.op, got, tc.want)
		}
	}
}

func TestShouldRecordFsEvent(t *testing.T) {
	keep := []fsnotify.Op{fsnotify.Write, fsnotify.Create, fsnotify.Remove, fsnotify.Rename}
	for _, op := range keep {
		if !shouldRecordFsEvent(fsnotify.Event{Op: op}) {
			t.Errorf("op %v should be recorded", op)
		}
	}
	if shouldRecordFsEvent(fsnotify.Event{Op: fsnotify.Chmod}) {
		t.Error("Chmod should be dropped (too noisy on macOS)")
	}
}

func TestProjectLabelStripsPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/code/tma1", "tma1"},
		{"/Users/x/code/tma1/", "tma1"},
		{"tma1", "tma1"},
	}
	for _, tc := range cases {
		if got := projectLabel(tc.in); got != tc.want {
			t.Errorf("projectLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
