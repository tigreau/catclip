package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestMetadataDiagnosticsUseEmittedPayloadForWarningThreshold(t *testing.T) {
	selection := output.Report{
		Sizes:     map[string]int64{},
		HumanSize: "2.64MB",
		Tokens:    691086,
		CountWord: "files",
	}
	metadata := &MetadataReport{
		Root:   "project",
		Scopes: []MetadataScope{{Summary: "."}},
		Rows:   []MetadataRow{{Path: "a.go", Size: "4.00B", Tokens: "~1", Git: "-", Modified: "Today"}},
	}
	var presentation, diagnostics bytes.Buffer
	proceed, err := WriteMetadataDiagnostics(
		RenderConfig{NoTree: true}, git.Context{}, output.Plan{}, selection, metadata,
		command.EmissionDefault, &presentation, &diagnostics, platform.Palette{}, platform.Palette{},
	)
	if err != nil {
		t.Fatalf("WriteMetadataDiagnostics: %v", err)
	}
	if !proceed {
		t.Fatal("small metadata payload should not require confirmation")
	}
	if strings.Contains(diagnostics.String(), "691086 tokens") || strings.Contains(diagnostics.String(), "Proceed?") {
		t.Fatalf("selected-content estimate incorrectly drove metadata confirmation: %q", diagnostics.String())
	}
}
