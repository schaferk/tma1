package git

import (
	"os"
	"path/filepath"
	"strings"
)

// gitignoreMatcher is a deliberately small subset of the .gitignore
// spec, tuned for "is this an artifact the project's own tooling
// already declared boring?" Anything more elaborate would compete with
// rg / VSCode / ag and isn't worth the complexity for our use case.
//
// Supported:
//   - directory patterns ending in "/" (e.g. "bin/")
//   - star-suffix globs ("*.log", "*.tmp")
//   - plain literal substrings ("CHANGELOG-archive")
//
// Skipped (treated as "no match"):
//   - negation rules starting with "!"
//   - anchored leading "/" semantics (we always do substring containment;
//     that's slightly more lenient than gitignore but matches what an
//     fs sensor actually wants)
//   - character classes [abc] and recursive ** patterns
//
// We prefer under-ignoring (real file event leaks through) to over-
// ignoring (real file event suppressed). The static ignore list and the
// attribution layer downstream both still see the path.
type gitignoreMatcher struct {
	dirs     []string // entries that ended with "/"; matched as "/<entry>/" substring
	suffixes []string // entries that started with "*"; matched as HasSuffix(path, entry-without-star)
	literals []string // everything else; matched as filepath.Base equality OR path substring
}

// loadGitignore parses <root>/.gitignore. Returns nil when the file is
// absent or unreadable — caller treats nil as "no per-project rules"
// and falls back to the static ignore list only.
func loadGitignore(root string) *gitignoreMatcher {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	return parseGitignore(string(data))
}

// parseGitignore is the pure parse step, kept separate so tests can
// hand it raw content without touching the filesystem.
func parseGitignore(content string) *gitignoreMatcher {
	m := &gitignoreMatcher{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			// Negation is rare and harder to get right than it looks --
			// skip entirely (lean toward under-ignoring).
			continue
		}
		// Strip any leading "/" -- gitignore's anchoring semantics are
		// stricter than what an fs sensor benefits from. We match by
		// substring throughout.
		line = strings.TrimPrefix(line, "/")
		switch {
		case strings.HasSuffix(line, "/"):
			m.dirs = append(m.dirs, strings.TrimSuffix(line, "/"))
		case strings.HasPrefix(line, "*"):
			m.suffixes = append(m.suffixes, line[1:])
		default:
			m.literals = append(m.literals, line)
		}
	}
	if len(m.dirs) == 0 && len(m.suffixes) == 0 && len(m.literals) == 0 {
		return nil
	}
	return m
}

// matches returns true when path should be ignored per the loaded
// patterns. root is the project root that the .gitignore lives in;
// matches uses it to normalise relative comparisons.
//
// Both inputs are normalised to forward slashes up front so the
// substring/prefix/suffix logic below stays a single POSIX-shaped
// path-handling path. fsnotify gives us OS-native separators on
// Windows, which would otherwise never match dir/literal patterns
// that were parsed as POSIX. filepath.ToSlash is OS-conditional
// (no-op on Unix), so we use ReplaceAll directly — that keeps unit
// tests for Windows paths reproducible on a macOS/Linux runner.
func (m *gitignoreMatcher) matches(path, root string) bool {
	if m == nil {
		return false
	}
	normPath := strings.ReplaceAll(path, "\\", "/")
	normRoot := strings.ReplaceAll(root, "\\", "/")
	rel := strings.TrimPrefix(normPath, normRoot)
	rel = strings.TrimPrefix(rel, "/")
	for _, d := range m.dirs {
		if strings.Contains(rel, d+"/") || strings.HasSuffix(rel, "/"+d) || rel == d {
			return true
		}
		// Catch top-level directory match: rel = "bin/foo" should match
		// dir entry "bin".
		if strings.HasPrefix(rel, d+"/") {
			return true
		}
	}
	for _, s := range m.suffixes {
		if strings.HasSuffix(normPath, s) {
			return true
		}
	}
	// Cross-platform basename: take everything after the last forward
	// slash on the already-normalised path. filepath.Base would use
	// the OS-native separator and miss backslash-separated input on
	// non-Windows runners.
	base := normPath
	if idx := strings.LastIndex(normPath, "/"); idx >= 0 {
		base = normPath[idx+1:]
	}
	for _, lit := range m.literals {
		if base == lit {
			return true
		}
		// Allow "foo/bar.txt" literal patterns to match by substring
		// against the relative path.
		if strings.Contains(rel, lit) {
			return true
		}
	}
	return false
}
