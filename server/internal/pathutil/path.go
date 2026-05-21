// Package pathutil holds tiny path helpers that need to be both
// POSIX- and Windows-aware. tma1-server runs on Linux/macOS but ingests
// `cwd` / `file_path` strings emitted by agents on every platform, so
// stdlib filepath.Base (which honours only the host separator) is
// insufficient — a Linux server would treat "C:\Users\foo\a.go" as one
// segment and label the project as the entire path.
//
// Kept as a separate package with zero deps so leaf packages (sensor/*)
// can import it without pulling in perception.
package pathutil

import "strings"

// Basename returns the last segment of p after the rightmost '/' or '\'.
// Trailing separators are trimmed first so "/a/b/" yields "b", matching
// filepath.Base. Returns the input unchanged when no separator is present.
func Basename(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Split returns the segments of p, splitting on both '/' and '\'. Empty
// leading / trailing / consecutive separators are dropped. Returns nil
// when p has no segments. Useful for "last N segments" rendering when
// the path may be from a foreign OS.
func Split(p string) []string {
	if p == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' || p[i] == '\\' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		out = append(out, p[start:])
	}
	return out
}
