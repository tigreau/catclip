package discovery

import "testing"

func TestLongestLiteralPathPrefix(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"*.go", ""},
		{"**/*.go", ""},
		{"cmd/*.go", "cmd"},
		{"cmd/**/*.go", "cmd"},
		{"internal/cli/*.go", "internal/cli"},
		{"internal/*/parse.go", "internal"},
		{"docs/", "docs/"}, // no glob chars — entire pattern is literal
		{"", ""},
		{"a/b/c/*", "a/b/c"},
		{"[abc]*.go", ""},
		{"?*.go", ""},
	}
	for _, tc := range cases {
		got := longestLiteralPathPrefix(tc.pattern)
		if got != tc.want {
			t.Errorf("longestLiteralPathPrefix(%q) = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

func TestOuterStarFuzzyCore(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    string
		ok      bool
	}{
		{pattern: "*util*", want: "util", ok: true},
		{pattern: "**auth**", want: "auth", ok: true},
		{pattern: "*layout/Footer*", want: "layout/Footer", ok: true},
		{pattern: "*/utils/*"},
		{pattern: "*../secret*"},
		{pattern: "*C:/secret*"},
		{pattern: "*foo?bar*"},
		{pattern: "***"},
		{pattern: "util*"},
	} {
		got, ok := outerStarFuzzyCore(tc.pattern)
		if got != tc.want || ok != tc.ok {
			t.Errorf("outerStarFuzzyCore(%q) = (%q, %t), want (%q, %t)", tc.pattern, got, ok, tc.want, tc.ok)
		}
	}
}
