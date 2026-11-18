package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestReferenceSuccessListsAttachedBinaries(t *testing.T) {
	stats := output.EmitStats{
		BundlePath:   `C:\Users\me\Documents\catclip\proj-124045.txt`,
		PayloadBytes: 15 * 1024,
		References: []output.BinaryReference{
			{Path: `C:\proj\bin\catclip`, RelPath: "bin/catclip", Size: 72},
			{Path: `C:\proj\bin\catclip-bench`, RelPath: "bin/catclip-bench", Size: 72},
		},
	}
	var out bytes.Buffer
	if err := writeReferenceSuccess(&out, len(stats.References), stats, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	order := []string{"Copied 3 file references to clipboard: 1 text bundle and 2 binary files\n", "proj-124045.txt (15KB)\n", "bin/catclip (72B)\n", "bin/catclip-bench (72B)\n", "Paste attaches the files"}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("missing or misplaced %q in:\n%s", want, got)
		}
		at += i + len(want)
	}
	if strings.Contains(got, "Clipboard:") {
		t.Fatal("the before-copy line was removed")
	}
}

func TestReferenceSuccessCapsLongLists(t *testing.T) {
	var stats output.EmitStats
	for i := 0; i < 8; i++ {
		stats.References = append(stats.References, output.BinaryReference{RelPath: fmt.Sprintf("img/%d.png", i), Size: 10})
	}
	var out bytes.Buffer
	if err := writeReferenceSuccess(&out, len(stats.References), stats, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.HasPrefix(got, "\nCopied 8 binary file references to clipboard\n") || !strings.Contains(got, "img/4.png") || strings.Contains(got, "img/5.png") || !strings.Contains(got, "...and 3 more\n") {
		t.Fatalf("got:\n%s", got)
	}
}
