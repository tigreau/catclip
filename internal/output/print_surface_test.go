package output

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/theme"
)

var surfaceTestControls = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)

func TestPrintSurfacePreservesTextAcrossWriteBoundaries(t *testing.T) {
	input := "header\r\n\n\x1b[32mgreen\x1b[0m normal\n\x1b[39;49mdefault\n" + strings.Repeat("界x", 80) + "\nlast"
	var reference string
	for size := 1; size <= len(input); size++ {
		var out bytes.Buffer
		_, err := WithTerminalPrintSurface(&out, platform.ANSIPalette(), func(w io.Writer) (EmitStats, error) {
			for at := 0; at < len(input); at += size {
				part := input[at:min(at+size, len(input))]
				if n, err := io.WriteString(w, part); err != nil || n != len(part) {
					t.Fatalf("write: %d %v", n, err)
				}
			}
			return EmitStats{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if size == 1 {
			reference = out.String()
		}
		if out.String() != reference {
			t.Fatalf("output depends on chunk size %d", size)
		}
		if got := surfaceTestControls.ReplaceAllString(out.String(), ""); got != surfaceTestControls.ReplaceAllString(input, "") {
			t.Fatalf("text changed: %q", got)
		}
	}
	if !strings.HasPrefix(reference, theme.Current().FG+theme.Current().BG+surfaceClearLine) || !strings.HasSuffix(reference, surfaceReset) {
		t.Fatal("missing surface or final reset")
	}
	if !strings.Contains(reference, "green"+surfaceReset+theme.Current().FG+theme.Current().BG+" normal") {
		t.Fatal("syntax reset lost the surface")
	}
	if strings.Contains(reference, "\r\x1b") {
		t.Fatal("inserted controls between CR and LF")
	}
}

func TestPrintSurfaceSGRDefaults(t *testing.T) {
	for _, tc := range []struct {
		sequence string
		fg, bg   bool
	}{
		{"", true, true}, {"0", true, true}, {"39", true, false}, {"49", false, true},
		{"0;32", false, true}, {"32;0", true, true}, {"0;31;44", false, false},
		{"38;5;0", false, false}, {"0;38;5;0", false, true},
		{"0;48;2;0;39;49", true, false}, {"38;2;0;0;0;49", false, true},
	} {
		fg, bg := surfaceDefaultsReset([]byte(tc.sequence))
		if fg != tc.fg || bg != tc.bg {
			t.Errorf("%q: got %t,%t want %t,%t", tc.sequence, fg, bg, tc.fg, tc.bg)
		}
	}
}

func TestPrintSurfaceEmptyAndDisabled(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		var out bytes.Buffer
		colors := platform.ANSIPalette()
		if disabled {
			colors = platform.Palette{}
		}
		_, err := WithTerminalPrintSurface(&out, colors, func(w io.Writer) (EmitStats, error) {
			_, _ = w.Write(nil)
			return EmitStats{}, ErrNoAccessibleOutput
		})
		if !errors.Is(err, ErrNoAccessibleOutput) || out.Len() != 0 {
			t.Fatalf("empty output modified: %q %v", out.String(), err)
		}
	}
	var out bytes.Buffer
	_, err := WithTerminalPrintSurface(&out, platform.Palette{}, func(w io.Writer) (EmitStats, error) { _, err := io.WriteString(w, "plain\n"); return EmitStats{}, err })
	if err != nil || out.String() != "plain\n" {
		t.Fatalf("disabled surface: %q %v", out.String(), err)
	}
}

type surfaceCountingSink struct{ bytes, calls int }

func (w *surfaceCountingSink) Write(p []byte) (int, error) {
	w.bytes += len(p)
	w.calls++
	return len(p), nil
}

func TestPrintSurfaceLargePayloadStaysStreamingAndCountsPlainBytes(t *testing.T) {
	payload := strings.Repeat("some text\n", 128*1024)
	sink := &surfaceCountingSink{}
	stats, err := WithTerminalPrintSurface(sink, platform.ANSIPalette(), func(w io.Writer) (EmitStats, error) {
		return WithPayloadWriter(EmitConfig{OutputMode: command.OutputModeStdout}, RuntimeEnvironment{}, w, platform.Palette{}, func(w io.Writer) error {
			_, err := io.WriteString(w, payload)
			if sink.calls == 0 {
				t.Fatal("buffered the entire payload")
			}
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.PayloadBytes != int64(len(payload)) {
		t.Fatalf("accounted presentation as payload: %+v", stats)
	}
	if sink.calls > sink.bytes/(32*1024)+2 {
		t.Fatalf("unbuffered terminal writes: %d", sink.calls)
	}
}

func TestPrintSurfaceResetsAfterInputFailure(t *testing.T) {
	var out bytes.Buffer
	failure := errors.New("input read failed")
	_, err := WithTerminalPrintSurface(&out, platform.ANSIPalette(), func(w io.Writer) (EmitStats, error) {
		return WithPayloadWriter(EmitConfig{OutputMode: command.OutputModeStdout}, RuntimeEnvironment{}, w, platform.Palette{}, func(w io.Writer) error {
			_, err := io.WriteString(w, strings.Repeat("payload\n", 128*1024))
			if err != nil {
				return err
			}
			return failure
		})
	})
	if !errors.Is(err, failure) || !strings.HasSuffix(out.String(), surfaceReset) {
		t.Fatal("input failure lost cleanup or cause")
	}
}

type surfaceFailingSink struct{}

func (surfaceFailingSink) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPrintSurfacePreservesDestinationFailure(t *testing.T) {
	_, err := WithTerminalPrintSurface(surfaceFailingSink{}, platform.ANSIPalette(), func(w io.Writer) (EmitStats, error) { _, err := io.WriteString(w, "text"); return EmitStats{}, err })
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost destination failure: %v", err)
	}
}

func BenchmarkPrintSurface(b *testing.B) {
	payload := bytes.Repeat([]byte("const example = 42;\n"), 64*1024)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := WithTerminalPrintSurface(io.Discard, platform.ANSIPalette(), func(w io.Writer) (EmitStats, error) { _, err := w.Write(payload); return EmitStats{}, err })
		if err != nil {
			b.Fatal(err)
		}
	}
}
