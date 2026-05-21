package pathutil

import (
	"reflect"
	"testing"
)

func TestBasenameHandlesBothSeparators(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"foo":                       "foo",
		"/a/b/c.go":                 "c.go",
		"/a/b/":                     "b",
		`C:\Users\dennis\a.go`:      "a.go",
		`C:\Users\dennis\`:          "dennis",
		`/a/b\c.go`:                 "c.go", // mixed — last sep wins
		"/":                         "",
		`\`:                         "",
		"justfile":                  "justfile",
		"/single":                   "single",
	}
	for in, want := range cases {
		if got := Basename(in); got != want {
			t.Errorf("Basename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitDropsEmptySegments(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"/", nil},
		{`\`, nil},
		{"a", []string{"a"}},
		{"/a/b/c", []string{"a", "b", "c"}},
		{"a/b/c/", []string{"a", "b", "c"}},
		{`C:\Users\dennis\a.go`, []string{"C:", "Users", "dennis", "a.go"}},
		{`/a\b/c`, []string{"a", "b", "c"}},
		{"a//b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := Split(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
