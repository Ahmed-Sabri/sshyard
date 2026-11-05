package vt

import "testing"

func TestLinkAt(t *testing.T) {
	for _, tc := range []struct {
		text string
		col  int
		want string
	}{
		{"see https://example.com/a?b=1 now", 10, "https://example.com/a?b=1"},
		{"see https://example.com/a?b=1 now", 4, "https://example.com/a?b=1"},
		{"see https://example.com/a?b=1 now", 2, ""},
		{"see https://example.com/a?b=1 now", 31, ""},
		{"(docs: http://x.io/p_(1)).", 12, "http://x.io/p_(1)"},
		{"(docs: http://x.io/p).", 12, "http://x.io/p"},
		{"go to https://a.b/c.", 10, "https://a.b/c"},
		{"\"https://a.b/q\"", 5, "https://a.b/q"},
		{"访问https://a.b/c查看", 6, "https://a.b/c"},
		{"xhttps://a.b", 4, ""},
		{"https:// nothing", 2, ""},
		{"ftp://a.b/c", 3, ""},
		{"HTTPS://A.B/C", 3, "HTTPS://A.B/C"},
		{"a https://one.io b https://two.io", 22, "https://two.io"},
	} {
		term := New(80, 3)
		term.WriteString(tc.text)
		s := term.Snapshot(0)
		// a column in cells: the CJK runes are two each
		col := tc.col
		for i, r := range []rune(tc.text) {
			if i >= tc.col {
				break
			}
			if r >= 0x80 {
				col++
			}
		}
		got, _, _ := term.LinkAt(Pos{s.First, col})
		if got != tc.want {
			t.Errorf("%q at %d: %q, want %q", tc.text, tc.col, got, tc.want)
		}
	}
}

// TestLinkAtWrapped: a link that wrapped is found from either row, with
// the cells it spans.
func TestLinkAtWrapped(t *testing.T) {
	term := New(10, 4)
	term.WriteString("> https://example.com/x y")
	s := term.Snapshot(0)
	for _, p := range []Pos{{s.First, 4}, {s.First + 1, 3}, {s.First + 2, 0}} {
		got, a, b := term.LinkAt(p)
		if got != "https://example.com/x" || a != (Pos{s.First, 2}) || b != (Pos{s.First + 2, 2}) {
			t.Errorf("at %v: %q from %v to %v", p, got, a, b)
		}
	}
	if got, _, _ := term.LinkAt(Pos{s.First + 2, 4}); got != "" {
		t.Errorf("after the link: %q", got)
	}
}
