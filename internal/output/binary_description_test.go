package output

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
)

func TestBinaryDescriptionsAndRawOutput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   []byte
		binary bool
	}{
		{"library.jar", []byte("PK\x00PRIVATE_CONTENT"), true},
		{"unknown.fixture", []byte("SECRET\x00TAIL"), true},
		{"empty.png", nil, false},
		{"text.fixture", []byte("readable\r\ntext"), false},
		{"utf16.fixture", []byte{0xff, 0xfe, 'a', 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name)
			if err := os.WriteFile(path, tc.body, 0600); err != nil {
				t.Fatal(err)
			}
			e := discovery.Entry{AbsPath: path, RelPath: tc.name, Mode: command.EntryModeFull}
			for _, raw := range []bool{false, true} {
				p, err := BuildPlanForResolvedScopes(git.Context{}, nil, nil, []discovery.Entry{e}, raw)
				if err != nil {
					t.Fatal(err)
				}
				var out bytes.Buffer
				if err = WriteOutputPlanPayloadWithoutPrefetch(&out, EmitConfig{Raw: raw}, p); err != nil {
					t.Fatal(err)
				}
				if raw {
					if !bytes.Equal(out.Bytes(), tc.body) {
						t.Fatalf("raw bytes changed: %q", out.Bytes())
					}
					continue
				}
				if tc.binary {
					body := BinaryDescription(tc.name, int64(len(tc.body)))
					want, _ := BuildWrappedPayload(tc.name, "binary", []byte(body))
					if !bytes.Equal(out.Bytes(), want) {
						t.Fatalf("got %q want %q", out.Bytes(), want)
					}
					_, size := p.PayloadSizes()
					if size != int64(len(body)) {
						t.Fatalf("counted %d instead of description size %d", size, len(body))
					}
					if got := p.EntriesInEmissionOrder()[0]; !got.Binary || got.SizeBytes != int64(len(tc.body)) {
						t.Fatalf("lost source metadata: %+v", got)
					}
					var preview bytes.Buffer
					if _, err := WriteOutputPlanPreview(&preview, EmitConfig{}, p, nil); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(preview.Bytes(), out.Bytes()) {
						t.Fatal("preview/final mismatch")
					}
				} else if strings.Contains(out.String(), `type="binary"`) {
					t.Fatal("text described as binary")
				}
			}
		})
	}
}

func TestBinaryDescriptionDoesNotReadKnownBinaryBody(t *testing.T) {
	// Retained size/type evidence is enough: no file exists at this path.
	e := discovery.Entry{AbsPath: filepath.Join(t.TempDir(), "missing.jar"), RelPath: "a&b.jar", Mode: command.EntryModeFull, SizeKnown: true, SizeBytes: 123, Binary: true}
	p, err := BuildPlanForResolvedScopes(git.Context{}, nil, nil, []discovery.Entry{e})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = WriteOutputPlanPayloadWithoutPrefetch(&out, EmitConfig{}, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Binary contents omitted.") || !strings.Contains(out.String(), "123 bytes") {
		t.Fatal(out.String())
	}
}

func BenchmarkBinaryDescriptionKnown(b *testing.B) {
	entries := []discovery.Entry{{AbsPath: "/retained/app.exe", RelPath: "app.exe", Mode: command.EntryModeFull, SizeKnown: true, SizeBytes: 20_000_000, Binary: true}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := BuildPlanForResolvedScopes(git.Context{}, nil, nil, entries); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBinaryDescriptionLinesAndUntrackedFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.fixture")
	if err := os.WriteFile(path, []byte("SECRET\x00CONTENTS"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []command.EntryMode{command.EntryModeLines, command.EntryModeDiff} {
		e := discovery.Entry{AbsPath: path, RelPath: "data.fixture", Mode: mode, Lines: true, LinesStart: 500, LinesEnd: 600}
		u, keep, err := PrepareFileUnit(git.Context{}, e, nil)
		if err != nil || !keep {
			t.Fatalf("%s: keep=%v err=%v", mode, keep, err)
		}
		if !strings.Contains(string(u.Payload), `type="binary"`) || bytes.Contains(u.Payload, []byte("SECRET")) {
			t.Fatalf("%s: unsafe payload %q", mode, u.Payload)
		}
		if mode == command.EntryModeLines && (u.Entry.Lines || u.Entry.LinesStart != 0 || u.Entry.LinesEnd != 0 || u.Entry.Mode == command.EntryModeLines) {
			t.Fatal("binary description still claims source lines")
		}
	}
}
