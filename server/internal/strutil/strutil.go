// Package strutil hosts small, dependency-free string helpers shared
// across the server packages. Created to centralise UTF-8-safe
// truncation: every SQL-quoting helper in sensor/{build,git,project}
// and perception used to slice byte-blindly with v[:maxLen], which can
// split a multi-byte rune and emit invalid UTF-8 into GreptimeDB.
package strutil

import "unicode/utf8"

// SafeTruncate returns s capped at at most maxBytes bytes, never
// splitting a multi-byte rune. When the byte slice boundary lands
// mid-rune we walk back to the nearest rune start. Pass maxBytes <= 0
// to get an empty string.
func SafeTruncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes to a rune boundary. utf8.RuneStart
	// returns true for the first byte of any UTF-8 sequence (including
	// 1-byte ASCII), so the loop terminates within at most 3 steps for
	// any valid UTF-8 input.
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
