package output

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

func stubFileclipCopy(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	previous := FileclipCopy
	FileclipCopy = func(paths ...string) error {
		calls = append(calls, append([]string(nil), paths...))
		return nil
	}
	t.Cleanup(func() { FileclipCopy = previous })
	return &calls
}

func binaryReferenceFixture(t *testing.T) (root string, text, image, archive discovery.Entry) {
	t.Helper()
	root = t.TempDir()
	write := func(name string, body []byte) discovery.Entry {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return discovery.Entry{AbsPath: path, RelPath: name}
	}
	text = write("src/main.go", []byte("package main\n"))
	image = write("assets/logo.png", []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 3})
	archive = write("lib/tool.jar", []byte{'P', 'K', 0, 0, 9, 9})
	t.Setenv("CATCLIP_BUNDLE_DIR", filepath.Join(root, "bundles"))
	return root, text, image, archive
}

func TestClipboardDeliversTextBundlePlusBinaryReferencesInOneCopy(t *testing.T) {
	root, text, image, archive := binaryReferenceFixture(t)
	calls := stubFileclipCopy(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{text, image, archive, image})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeClipboard}, RuntimeEnvironment{WorkingDir: root}, plan, &bytes.Buffer{}, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || len((*calls)[0]) != 3 {
		t.Fatalf("clipboard calls: %v", *calls)
	}
	offered := (*calls)[0]
	if offered[0] != stats.BundlePath || stats.SinkName != "bundle" || !reflect.DeepEqual(offered[1:], []string{image.AbsPath, archive.AbsPath}) {
		t.Fatalf("offered %v stats %+v", offered, stats)
	}
	if !reflect.DeepEqual(stats.ReferencePaths(), []string{image.AbsPath, archive.AbsPath}) {
		t.Fatalf("references %v", stats.ReferencePaths())
	}
	// The tiny text still becomes a bundle, and the bundle maps every
	// attachment back to its project path without carrying binary bytes.
	bundle, err := os.ReadFile(stats.BundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle) >= BundleThreshold {
		t.Fatalf("fixture should stay below the size threshold: %d", len(bundle))
	}
	if strings.Contains(string(bundle), "Binary contents omitted.") {
		t.Fatalf("attached binaries must not be described as omitted:\n%s", bundle)
	}
	for _, want := range []string{`<file path="src/main.go">`, "package main", `path="assets/logo.png"`, `path="lib/tool.jar"`,
		`Attached separately as the original file "logo.png".`, `Attached separately as the original file "tool.jar".`} {
		if !strings.Contains(string(bundle), want) {
			t.Fatalf("bundle lacks %q:\n%s", want, bundle)
		}
	}
	if bytes.Contains(bundle, []byte{0x89, 'P', 'N', 'G', 0}) {
		t.Fatal("bundle contains binary bytes")
	}
}

func TestClipboardBinaryOnlySelectionCreatesNoTextBundle(t *testing.T) {
	root, _, image, _ := binaryReferenceFixture(t)
	calls := stubFileclipCopy(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{image})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeClipboard}, RuntimeEnvironment{WorkingDir: root}, plan, &bytes.Buffer{}, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*calls, [][]string{{image.AbsPath}}) || stats.BundlePath != "" || stats.SinkName != "references" {
		t.Fatalf("calls %v stats %+v", *calls, stats)
	}
	if _, err := os.Stat(filepath.Join(root, "bundles")); !os.IsNotExist(err) {
		t.Fatalf("a bundle directory was created: %v", err)
	}
}

func TestBinaryReferencesOnlyApplyToDefaultClipboardFileContents(t *testing.T) {
	_, text, image, _ := binaryReferenceFixture(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{text, image})
	if err != nil {
		t.Fatal(err)
	}
	clipboard := EmitConfig{OutputMode: command.OutputModeClipboard}
	if cfg := clipboard.WithBinaryReferences(plan); len(cfg.references) != 1 || cfg.referencesOnly {
		t.Fatalf("%+v", cfg)
	}
	for name, cfg := range map[string]EmitConfig{
		"--no-bundle": {OutputMode: command.OutputModeClipboard, NoBundle: true},
		"--raw":       {OutputMode: command.OutputModeClipboard, Raw: true},
		"stdout":      {OutputMode: command.OutputModeStdout},
	} {
		if got := cfg.WithBinaryReferences(plan); len(got.references) != 0 {
			t.Fatalf("%s attached %v", name, got.references)
		}
	}
	// Path lists describe files; they never become attachments.
	paths := Plan{items: []PlanItem{newPathOutputPlanItem(image)}}
	if refs, textual := paths.BinaryReferences(); len(refs) != 0 || !textual {
		t.Fatalf("paths plan: %v %t", refs, textual)
	}
	// Text-only plans keep the size-based sink.
	textOnly, err := PreparePlan(git.Context{}, []discovery.Entry{text})
	if err != nil {
		t.Fatal(err)
	}
	if cfg := clipboard.WithBinaryReferences(textOnly); len(cfg.references) != 0 {
		t.Fatalf("%+v", cfg)
	}
}

func TestBinaryReferencesFollowTheTokenLimit(t *testing.T) {
	root, text, image, archive := binaryReferenceFixture(t)
	calls := stubFileclipCopy(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{text, image, archive})
	if err != nil {
		t.Fatal(err)
	}
	// Budget for the text file and the image description only; the archive's
	// description is cut, so its original file is not attached.
	sizes, _ := plan.PayloadSizes()
	budget := (sizes[text.RelPath] + sizes[image.RelPath] + 2) / 4
	stats, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeClipboard, TokenLimit: budget}, RuntimeEnvironment{WorkingDir: root}, plan, &bytes.Buffer{}, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stats.ReferencePaths(), []string{image.AbsPath}) || len(*calls) != 1 || len((*calls)[0]) != 2 {
		t.Fatalf("references %v calls %v", stats.ReferencePaths(), *calls)
	}
}

func TestVanishedBinaryIsNotOfferedToTheClipboard(t *testing.T) {
	root, text, image, _ := binaryReferenceFixture(t)
	calls := stubFileclipCopy(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{text, image})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(image.AbsPath); err != nil {
		t.Fatal(err)
	}
	stats, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeClipboard}, RuntimeEnvironment{WorkingDir: root}, plan, &bytes.Buffer{}, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.References) != 0 || len(*calls) != 1 || len((*calls)[0]) != 1 || (*calls)[0][0] != stats.BundlePath {
		t.Fatalf("references %v calls %v", stats.References, *calls)
	}
}

func TestBinaryReferencesCarryProjectPathAndSize(t *testing.T) {
	root, text, image, _ := binaryReferenceFixture(t)
	stubFileclipCopy(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{text, image})
	if err != nil {
		t.Fatal(err)
	}
	stats, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeClipboard}, RuntimeEnvironment{WorkingDir: root}, plan, &bytes.Buffer{}, platform.Palette{})
	if err != nil {
		t.Fatal(err)
	}
	want := []BinaryReference{{Path: image.AbsPath, RelPath: "assets/logo.png", Size: 8}}
	if !reflect.DeepEqual(stats.References, want) {
		t.Fatalf("%+v", stats.References)
	}
}

func TestTextOutputsKeepOmittedWordingAndThePlanIsNotChanged(t *testing.T) {
	root, text, image, _ := binaryReferenceFixture(t)
	stubFileclipCopy(t)
	plan, err := PreparePlan(git.Context{}, []discovery.Entry{text, image})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeClipboard}, RuntimeEnvironment{WorkingDir: root}, plan, &bytes.Buffer{}, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	// The same retained plan, emitted to stdout afterwards, still describes
	// the binary as omitted: the clipboard rewrite worked on a copy.
	var stdout bytes.Buffer
	if _, err := EmitOutputPlan(EmitConfig{OutputMode: command.OutputModeStdout}, RuntimeEnvironment{WorkingDir: root}, plan, &stdout, platform.Palette{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Binary contents omitted.") || strings.Contains(stdout.String(), "Attached separately") {
		t.Fatalf("stdout:\n%s", stdout.String())
	}
}

// A large text plan with a few attached binaries: the bundle rewrite should
// cost microseconds, not a pass over file contents.
func BenchmarkAttachedBinaryDescriptions(b *testing.B) {
	var units []PreparedFileUnit
	for i := 0; i < 10000; i++ {
		units = append(units, PreparedFileUnit{Entry: discovery.Entry{RelPath: fmt.Sprintf("src/f%d.go", i)}, BodyBytes: 100})
	}
	var refs []BinaryReference
	for i := 0; i < 3; i++ {
		rel := fmt.Sprintf("img/%d.png", i)
		payload, n := BuildWrappedPayload(rel, "binary", []byte(BinaryDescription(rel, 1<<20)))
		units = append(units, PreparedFileUnit{Entry: discovery.Entry{RelPath: rel, SizeBytes: 1 << 20, SizeKnown: true}, Payload: payload, BodyBytes: n, sourceType: "binary"})
		refs = append(refs, BinaryReference{Path: rel, RelPath: rel, Size: 1 << 20})
	}
	plan := BuildPlan(units)
	b.Run("no binaries attached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = plan.withAttachedBinaryDescriptions(nil)
		}
	})
	b.Run("3 of 10003 attached", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = plan.withAttachedBinaryDescriptions(refs)
		}
	})
}
