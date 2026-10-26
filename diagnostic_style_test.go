package catclip

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/platform"
)

func TestDiagnosticColorsPreserveErrorText(t *testing.T) {
	colors := platform.ANSIPalette()
	for _, message := range []string{
		"Error: '.env' is a bare extension, not a target.\n  Use it as a glob target:\n    catclip '*.env'\n\n  Found an ignored file named '.env'. To select it directly:\n    catclip services/api/.env",
		cli.BareModifierPlaceholderHeadlessModeError().Error(),
		"Error: invalid range.\n  Example: catclip src --lines 1 20\n  Try: catclip --help\n  See every active rule:   catclip --all-ignore-rules",
		"open src/main.go: permission denied",
		"Error: first\n\nError: second\n\tExplanation.\n\tcatclip src",
	} {
		got := styleDiagnosticMessage(message, colors, true)
		if !strings.Contains(got, colors.Err) {
			t.Fatalf("missing error color: %q", got)
		}
		if plain := stripDiagnosticSGR(got); plain != message {
			t.Fatalf("changed wording: %q want %q", plain, message)
		}
		if got := styleDiagnosticMessage(message, platform.Palette{}, true); got != message {
			t.Fatalf("plain changed: %q", got)
		}
	}
	message := "Error: failed\n  Guidance.\n    catclip src\n  Example: catclip docs"
	got := styleDiagnosticMessage(message, colors, true)
	for _, fragment := range []string{colors.Bold + colors.Err + "Error:", colors.Dim + "Guidance.", colors.OK + "catclip src", colors.OK + "catclip docs"} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("missing %q in %q", fragment, got)
		}
	}
}

func TestDiagnosticHeadlessRespectsFlagOperands(t *testing.T) {
	colors := platform.ANSIPalette()
	for _, tc := range []struct {
		args     []string
		headless bool
	}{
		{[]string{".env", "--headless"}, true},
		{[]string{"--headless", ".env"}, true},
		{[]string{"src", "--then", "docs", "--headless"}, true},
		{[]string{"src", "--contains", "--headless"}, false},
		{[]string{"src", "--snippet", "--headless"}, false},
		{[]string{"src", "--contains", "--headless", "--headless"}, true},
		{[]string{"--internal-sink-preview", "mode", "--headless", "value"}, false},
	} {
		if got := diagnosticHeadless(tc.args); got != tc.headless {
			t.Fatalf("args=%q got=%v", tc.args, got)
		}
		got := styleErrorMessage("Error: failed", tc.args, colors)
		if strings.Contains(got, "\x1b[") == tc.headless {
			t.Fatalf("wrong colors: args=%q text=%q", tc.args, got)
		}
	}
	styled := colors.Err + "Error: already styled" + colors.Reset
	if got := styleErrorMessage(styled, []string{".", "--headless"}, colors); got != "Error: already styled" {
		t.Fatalf("headless ANSI leaked: %q", got)
	}
}

func TestDiagnosticColorsRespectExistingStylingAndPlainRoutes(t *testing.T) {
	colors := platform.ANSIPalette()
	styled := colors.Bold + colors.Err + "Error:" + colors.Reset + " ignored\n  " + colors.OK + "catclip path/.env" + colors.Reset
	if got := styleDiagnosticMessage(styled, colors, true); got != styled {
		t.Fatalf("double styling: %q", got)
	}
	plain := "Error: ignored\n  catclip path/.env"
	var stderr bytes.Buffer
	if got := styleErrorMessage(styled, nil, platform.ActivePaletteForWriter(&stderr)); got != plain {
		t.Fatalf("redirect retained ANSI: %q", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := styleErrorMessage(styled, nil, platform.ActivePalette()); got != plain {
		t.Fatalf("NO_COLOR retained ANSI: %q", got)
	}
}

func TestDiscoveryDiagnosticColorsAndWriterErrors(t *testing.T) {
	colors := platform.ANSIPalette()
	message := "Warning: missing target.\n  Try: catclip src"
	var stderr bytes.Buffer
	if err := writeDiscoveryDiagnostics([]discovery.Diagnostic{{Message: message}}, &stderr, colors); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), colors.Warn+"Warning:") || !strings.Contains(stderr.String(), colors.OK+"catclip src") {
		t.Fatalf("uncolored diagnostic: %q", stderr.String())
	}
	if got := stripDiagnosticSGR(stderr.String()); got != message+"\n" {
		t.Fatalf("changed message: %q", got)
	}
}

func TestStripDiagnosticSGRPreservesOtherBytes(t *testing.T) {
	input := "a\x1b[1;31mb\x1b[0mc\x1b[38:2:1:2:3md\x1b[me\x1b[bad"
	if got := stripDiagnosticSGR(input); got != "abcde\x1b[bad" {
		t.Fatalf("got %q", got)
	}
}
