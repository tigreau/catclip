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
