package strutil

import "testing"

func TestSafeTruncatePreservesUTF8Boundaries(t *testing.T) {
	// Each 中 / 文 is 3 bytes (E4 B8 AD / E6 96 87) in UTF-8.
	// € is 3 bytes (E2 82 AC).
	// Naive v[:n] can split a rune mid-sequence and emit invalid UTF-8.
	cases := []struct {
		in       string
		maxBytes int
		want     string
	}{
		// ASCII baseline.
		{"hello", 3, "hel"},
		{"hello", 10, "hello"},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"", 5, ""},

		// Pure multi-byte: rune boundaries vs. mid-rune.
		{"中文", 6, "中文"}, // fits exactly
		{"中文", 5, "中"},  // mid-rune (between byte 3 and 6) -> back to "中"
		{"中文", 4, "中"},  // same
		{"中文", 3, "中"},  // exact boundary at 3
		{"中文", 2, ""},    // can't keep one full rune

		// Mixed: ASCII + multi-byte. "a中b" = 61 E4 B8 AD 62 (5 bytes).
		{"a中b", 5, "a中b"},
		{"a中b", 4, "a中"},
		{"a中b", 3, "a"}, // mid-rune at byte 3 (inside 中) -> back to "a"
		{"a中b", 1, "a"},

		// Leading multi-byte then ASCII. "€uro" = E2 82 AC 75 72 6F.
		// maxBytes=4 lands on 0x75 ('u') which IS a rune start, so we
		// keep s[:4] = "€u". The rune-start walk only triggers when
		// the boundary byte is a UTF-8 continuation.
		{"€uro", 4, "€u"},
		{"€uro", 3, "€"},
		{"€uro", 2, ""}, // would split € mid-rune -> empty
	}

	for _, c := range cases {
		got := SafeTruncate(c.in, c.maxBytes)
		if got != c.want {
			t.Errorf("SafeTruncate(%q, %d) = %q, want %q",
				c.in, c.maxBytes, got, c.want)
		}
	}
}
