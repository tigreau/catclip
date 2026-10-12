package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/platform"
)

// The transient preview must not claim that cached search work cannot recur,
// or that no matches exist while the completed result's preview is loading.
func TestBuildInternalSearchingHintDocument(t *testing.T) {
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			var buf bytes.Buffer
			if err := renderTreeDocument(&buf, buildInternalSearchingHintDocument(goos), FzfFilterTreeRenderOptions(), platform.ANSIPalette()); err != nil {
				t.Fatal(err)
			}
			text := string(ansiEscape.ReplaceAll(buf.Bytes(), nil))
			if !strings.Contains(text, "Searching file contents") {
				t.Fatalf("missing search progress: %q", text)
			}
			if strings.Contains(text, "Microsoft Defender may inspect files") != (goos == "windows") {
				t.Fatalf("wrong platform explanation: %q", text)
			}
			for _, misleading := range []string{"no matches", "reboot", "~30s", "will not recur"} {
				if strings.Contains(text, misleading) {
					t.Fatalf("search progress contains %q: %q", misleading, text)
				}
			}
		})
	}
}
