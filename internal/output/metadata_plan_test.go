package output

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

func TestBuildMetadataPlanUsesCapturedMetadataWithoutOpeningBodies(t *testing.T) {
	root := t.TempDir()
	stamp := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	inv := discovery.Discovered{Scopes: []discovery.Scope{{
		Scope: command.ExecutionScope{Targets: []string{"."}},
		Entries: []discovery.Entry{
			{RelPath: "b.go", AbsPath: filepath.Join(root, "missing-b.go"), SizeBytes: 22, SizeKnown: true, ModTime: stamp},
			{RelPath: "a.go", AbsPath: filepath.Join(root, "missing-a.go"), SizeBytes: 11, SizeKnown: true, ModTime: stamp},
		},
	}}}

	plan, err := BuildMetadataPlanForDiscoveredInvocation(root, inv)
	if err != nil {
		t.Fatalf("BuildMetadataPlanForDiscoveredInvocation: %v", err)
	}
	entries := plan.EntriesInEmissionOrder()
	if len(entries) != 2 || entries[0].RelPath != "a.go" || entries[1].RelPath != "b.go" {
		t.Fatalf("metadata plan order = %#v, want a.go then b.go", entries)
	}
	for _, entry := range entries {
		if !entry.SizeKnown || entry.ModTime.IsZero() {
			t.Fatalf("metadata plan lost captured metadata: %#v", entry)
		}
	}
	sizes, total := plan.PayloadSizes()
	if sizes["a.go"] != 11 || sizes["b.go"] != 22 || total != 33 {
		t.Fatalf("metadata plan sizes = %#v total=%d, want 11/22 total 33", sizes, total)
	}
}
