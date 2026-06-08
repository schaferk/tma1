package git

import (
	"os"
	"path/filepath"
	"strconv"
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
		{"/repo/bin/tma1-server", true}, // dogfood report: bin/ was leaking
		{"/repo/out/foo.class", true},
		{"/repo/vendor/x.go", true},
		{"/repo/.venv/bin/python", true},
		{"/repo/.tma1/state.json", true}, // tma1's own data dir
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
		// Root-level macOS system trees — matched by prefix.
		{"/Applications/Foo.app/Contents/MacOS/foo", true},
		{"/Library/Caches/x", true},
		{"/System/Library/Frameworks/x", true},
		// A project named or containing "Library"/"System"/"Applications" must
		// still be watched — prefix matching only blocks the real OS trees at
		// "/", not same-named subdirs (Unity Library/, ECS System/, ...).
		{"/Users/dennis/code/Library/src/main.go", false},
		{"/Users/dennis/code/game/System/world.go", false},
		{"/Users/dennis/Library/Caches/y", false}, // ~/Library: P0's job, not this guard
		// External disks are NOT blocked — projects legitimately live there.
		{"/Volumes/Disk/proj/src/main.go", false},
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

func TestAddRecursiveRespectsCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		if err := os.MkdirAll(filepath.Join(root, "d"+strconv.Itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("cap truncates the walk", func(t *testing.T) {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		defer fsw.Close()

		added, stopped, err := addRecursive(fsw, root, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !stopped {
			t.Fatal("stopped = false, want true")
		}
		if added != 3 {
			t.Errorf("added = %d, want 3 (cap reached, 11 dirs available)", added)
		}
	})

	t.Run("ample cap watches root + all subdirs", func(t *testing.T) {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		defer fsw.Close()

		added, stopped, err := addRecursive(fsw, root, 100)
		if err != nil {
			t.Fatal(err)
		}
		if stopped {
			t.Fatal("stopped = true, want false")
		}
		if added != 11 { // root + 10 subdirs
			t.Errorf("added = %d, want 11 (root + 10 subdirs)", added)
		}
	})

	t.Run("cap still stops when add fails", func(t *testing.T) {
		fsw, err := fsnotify.NewWatcher()
		if err != nil {
			t.Fatal(err)
		}
		if err := fsw.Close(); err != nil {
			t.Fatal(err)
		}

		added, stopped, err := addRecursive(fsw, root, 3)
		if err != nil {
			t.Fatal(err)
		}
		if !stopped {
			t.Fatal("stopped = false, want true")
		}
		if added != 0 {
			t.Errorf("added = %d, want 0 after watcher is closed", added)
		}
	})
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
