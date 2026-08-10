package git

import "testing"

// TestParseStatusMapPreservesLeadingSpaceOnFirstLine guards against a
// regression: porcelain lines for unstaged-modified entries begin with
// a literal space (xy[0]=' ', xy[1]='M'). An earlier parseStatusMap
// implementation called strings.TrimSpace on the whole output before
// splitting, which silently stripped that leading space from the first
// such line — shifting line[3:] by one byte and producing a truncated
// path classified as staged instead of unstaged. The bug only surfaced
// when porcelain output had exactly one entry and that entry was
// unstaged-modified, which happens when StatusMapForPathspecs is
// called with a single-file pathspec.
func TestParseStatusMapPreservesLeadingSpaceOnFirstLine(t *testing.T) {
	ctx := Context{Enabled: true}
	output := " M unstaged.txt\n"
	got := parseStatusMap(ctx, output)
	want := map[string]string{"unstaged.txt": "M"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestParseStatusMapHandlesAllStatusKinds(t *testing.T) {
	ctx := Context{Enabled: true}
	output := "M  staged.txt\n M unstaged.txt\nMM both.txt\n?? new.txt\n"
	got := parseStatusMap(ctx, output)
	want := map[string]string{
		"staged.txt":   "S",
		"unstaged.txt": "M",
		"both.txt":     "SM",
		"new.txt":      "?",
	}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %q: want %q, got %q (full=%v)", k, v, got[k], got)
		}
	}
}

func TestParseStatusMapEmptyInput(t *testing.T) {
	ctx := Context{Enabled: true}
	got := parseStatusMap(ctx, "")
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestParseStatusMapHandlesRenames(t *testing.T) {
	ctx := Context{Enabled: true}
	// Rename in porcelain is "R  oldpath -> newpath"; parser takes the
	// rightmost path (the new one).
	output := "R  old.txt -> new.txt\n"
	got := parseStatusMap(ctx, output)
	if got["new.txt"] != "S" {
		t.Fatalf("rename should map to new path with staged status; got %v", got)
	}
	if _, ok := got["old.txt"]; ok {
		t.Fatalf("old path should not appear in status map; got %v", got)
	}
}

func TestParseRootAndPrefix(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantRoot   string
		wantPrefix string
	}{
		{name: "repo root LF", output: "/repo\n\n", wantRoot: "/repo"},
		{name: "nested LF", output: "/repo\nsrc/app/\n", wantRoot: "/repo", wantPrefix: "src/app/"},
		{name: "nested CRLF", output: "C:\\repo\r\nsrc/app/\r\n", wantRoot: `C:\repo`, wantPrefix: "src/app/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, prefix := parseRootAndPrefix(tc.output)
			if root != tc.wantRoot || prefix != tc.wantPrefix {
				t.Fatalf("parseRootAndPrefix(%q) = (%q, %q), want (%q, %q)", tc.output, root, prefix, tc.wantRoot, tc.wantPrefix)
			}
		})
	}
}
