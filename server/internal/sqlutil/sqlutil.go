// Package sqlutil hosts the GreptimeDB SQL string-quoting helpers
// shared across perception, handler, and sensor packages.
//
// Why centralise: we previously had five near-identical copies of
// "double single quotes" and "escape LIKE wildcards" -- one per
// package -- which made a fix-once-fix-everywhere mistake easy and
// guaranteed eventual drift. The functions here are the single source
// of truth; callers should not roll their own.
//
// All three primitives assume the literal will be embedded inside
// single-quoted SQL strings. LIKE patterns rely on GreptimeDB's
// default escape character — backslash — so callers do NOT need an
// `ESCAPE` clause; using one with any other char fails at parse time
// ("LIKE does not support escape_char other than the backslash").
package sqlutil

import (
	"strings"

	"github.com/tma1-ai/tma1/server/internal/strutil"
)

// Escape returns s safe to interpolate inside a single-quoted SQL
// string literal. The only character that needs escaping inside a
// single-quoted literal is the single quote itself, which is doubled
// per SQL standard.
//
// Caller is still responsible for wrapping the result in '...'.
func Escape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// EscapeLike escapes s for use as a literal inside a LIKE pattern,
// then applies Escape so the result is safe inside a single-quoted
// SQL literal too. GreptimeDB's LIKE only accepts backslash as the
// escape character, so backslash is what we emit; no `ESCAPE` clause
// is needed in the SQL.
//
// Use whenever an unsanitised file_path / command / project name /
// other agent-controlled string is interpolated into a LIKE pattern;
// otherwise a path containing '%' or '_' silently over-matches.
func EscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`) // backslash itself first
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return strings.ReplaceAll(s, "'", "''")
}

// Quote returns the SQL literal NULL for an empty input, otherwise a
// single-quoted SQL string with embedded quotes escaped and the
// value truncated rune-safely to at most maxBytes bytes.
//
// Use this for every column that's NULL when empty but a quoted
// string otherwise -- the rune-safe truncation keeps GreptimeDB from
// being fed invalid UTF-8 when an agent passes non-ASCII content.
func Quote(v string, maxBytes int) string {
	if v == "" {
		return "NULL"
	}
	return "'" + Escape(strutil.SafeTruncate(v, maxBytes)) + "'"
}
