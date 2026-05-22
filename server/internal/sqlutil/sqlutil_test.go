package sqlutil

import "testing"

func TestEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"O'Brien", "O''Brien"},
		{"a'b'c", "a''b''c"},
	}
	for _, c := range cases {
		if got := Escape(c.in); got != c.want {
			t.Errorf("Escape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"50%", `50\%`},
		{"a_b", `a\_b`},
		{`back\slash`, `back\\slash`},
		{"bang!", "bang!"}, // '!' no longer special — passes through.
		{`O'Brien_%\`, `O''Brien\_\%\\`},
	}
	for _, c := range cases {
		if got := EscapeLike(c.in); got != c.want {
			t.Errorf("EscapeLike(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuote(t *testing.T) {
	cases := []struct{ name, in, want string; max int }{
		{"empty becomes NULL", "", "NULL", 100},
		{"plain", "hello", "'hello'", 100},
		{"single quotes doubled", "O'Brien", "'O''Brien'", 100},
		{"truncates to maxBytes", "abcdef", "'abc'", 3},
		// Rune-safe: 4-byte rune at boundary stays whole. ASCII a (1) +
		// 𝕏 = U+1D54F = 4 bytes. maxBytes=3 ⇒ must drop the rune.
		{"rune-safe drop", "a𝕏", "'a'", 3},
	}
	for _, c := range cases {
		if got := Quote(c.in, c.max); got != c.want {
			t.Errorf("%s: Quote(%q, %d) = %q, want %q", c.name, c.in, c.max, got, c.want)
		}
	}
}
