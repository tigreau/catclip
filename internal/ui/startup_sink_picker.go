package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/picker"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
)

func RunInternalSinkToggle(modePath string, stdout io.Writer) error {
	mode, err := os.ReadFile(modePath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(mode)) == "tree" {
		return os.WriteFile(modePath, []byte("output\n"), 0o600)
	}
	return os.WriteFile(modePath, []byte("tree\n"), 0o600)
}

func RunInternalSinkPreview(modePath, outputPath, treePath string, stdout io.Writer) error {
	finishBench := platform.InternalBenchSpan("ui.internal.sink_preview")
	finishModeBench := platform.InternalBenchSpan("ui.internal.sink_preview.read_mode")
	mode, err := os.ReadFile(modePath)
	finishModeBench("err", platform.InternalBenchError(err))
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	targetPath := outputPath
	modeName := "output"
	if strings.TrimSpace(string(mode)) == "tree" {
		targetPath = treePath
		modeName = "tree"
	}
	finishContentBench := platform.InternalBenchSpan("ui.internal.sink_preview.read_target",
		"mode", modeName,
	)
	content, err := os.ReadFile(targetPath)
	finishContentBench(
		"err", platform.InternalBenchError(err),
		"bytes", platform.InternalBenchInt(len(content)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return err
	}
	_, err = stdout.Write(content)
	finishBench(
		"err", platform.InternalBenchError(err),
		"mode", modeName,
		"bytes", platform.InternalBenchInt(len(content)),
	)
	return err
}

type StartupPreparedOutputState struct {
	Git       git.Context
	Discovery discovery.Result
	Plan      output.Plan
}

type startupSinkChoice struct {
	Key         string
	Label       string
	Description string
	Args        []string
}

type sinkPayloadMeasurement struct {
	Bytes       int64
	WouldBundle bool
	Err         error
}

type sinkPreviewMode int

const (
	sinkPreviewModeOutputText sinkPreviewMode = iota
	sinkPreviewModeTreeReport
)

type sinkPreview struct {
	Mode           sinkPreviewMode
	Body           []byte
	Truncated      bool
	FullBytesKnown bool
	FullBytes      int64
}

type StartupSinkPickerContext struct {
	Config    command.Parsed
	Emit      output.EmitConfig
	Render    RenderConfig
	Git       git.Context
	Discovery discovery.Result
	Plan      output.Plan
	Report    output.Report
}

type startupSinkPreviewFiles struct {
	PreviewCommand string
	ToggleBinding  string
	Cleanup        func()
}

var startupSinkChoicesSmall = []startupSinkChoice{
	{
		Key:         "clipboard",
		Label:       "Clipboard",
		Description: "Copy result to the system clipboard as text.",
	},
	{
		Key:         "stdout",
		Label:       "Stdout - for piping",
		Description: "Print to stdout for piping into another tool or saving to a file.",
		Args:        []string{"-p"},
	},
	{
		Key:         "preview",
		Label:       "Stdout - metadata only",
		Description: "Print paths, sizes, tokens, git, dates.",
		Args:        []string{"--preview"},
	},
	{
		Key:         "headless",
		Label:       "Headless - agent / script contract",
		Description: "Print to stdout with quiet stderr and no prompts.",
		Args:        []string{"--headless"},
	},
}

var startupSinkChoicesLarge = []startupSinkChoice{
	{
		Key:         "bundle",
		Label:       "Clipboard - bundle as file",
		Description: "Bundle output into a temp file and place a file reference on the clipboard.",
	},
	{
		Key:         "text",
		Label:       "Clipboard - text only",
		Description: "Force text clipboard even when the payload is large.",
		Args:        []string{"--no-bundle"},
	},
	{
		Key:         "stdout",
		Label:       "Stdout - for piping",
		Description: "Print to stdout for piping into another tool or saving to a file.",
		Args:        []string{"-p"},
	},
	{
		Key:         "preview",
		Label:       "Stdout - metadata only",
		Description: "Print paths, sizes, tokens, git, dates.",
		Args:        []string{"--preview"},
	},
	{
		Key:         "headless",
		Label:       "Headless - agent / script contract",
		Description: "Print to stdout with quiet stderr and no prompts.",
		Args:        []string{"--headless"},
	},
}

func maybeResolveStartupSinkPickerArgs(rawArgs []string, result StartupPickerResult) (StartupPickerResult, error) {
	if !result.UsedFzf || len(result.Args) == 0 || rawArgsSkipOutputSinkPicker(rawArgs) {
		return result, nil
	}

	ctx, err := buildStartupSinkPickerContext(result.Args)
	if err != nil {
		return StartupPickerResult{}, err
	}
	prepared := &StartupPreparedOutputState{
		Git:       ctx.Git,
		Discovery: ctx.Discovery,
		Plan:      ctx.Plan,
	}
	// There is no meaningful destination or preview for an empty payload.
	// Preserve the prepared discovery so normal execution prints its standard
	// diagnostics and exit code without opening a blank sink picker.
	if ctx.Plan.IsEmpty() {
		out := result
		out.PreparedOutput = prepared
		return out, nil
	}
	measurement := measureOutputForSinkMenu(ctx.Plan, ctx.Emit)
	sinkArgs, usedFzf, err := pickOutputSink(ctx, measurement)
	if err != nil {
		return StartupPickerResult{}, err
	}

	out := result
	out.Args = append(append([]string(nil), result.Args...), sinkArgs...)
	out.UsedFzf = out.UsedFzf || usedFzf
	out.ForceResolvedCommand = out.ForceResolvedCommand || (argsContain(sinkArgs, "--headless") && !rawArgsRequestQuiet(rawArgs))
	out.PreparedOutput = prepared
	return out, nil
}

func rawArgsSkipOutputSinkPicker(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-p", "--print", "--headless", "--no-bundle", "--preview", "-q", "--quiet":
			return true
		}
	}
	return false
}

func argsContain(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func buildStartupSinkPickerContext(args []string) (StartupSinkPickerContext, error) {
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return StartupSinkPickerContext{}, err
	}
	emitCfg := emitConfigFromParsedCommand(cfg)
	renderCfg := RenderConfigFromParsedCommand(cfg)
	renderCfg.Preview = true
	gitCtx := git.Detect(cfg.WorkingDir)
	// Discovery diagnostics from this pass get embedded in the prepared
	// state and replayed by run() after the picker resolves. They must
	// carry the same ANSI codes a non-interactive run would produce; an
	// empty palette here would strip color from every discovery.Diagnostic
	// (ignored-target errors, target-not-found warnings, etc.) the user
	// later sees on stderr after picker confirmation.
	colors := platform.ActivePalette()

	discoveryResult, err := discovery.DiscoverInvocation(resolvedInvocationFromParsedCommand(cfg), gitCtx, io.Discard, colors)
	if err != nil {
		return StartupSinkPickerContext{}, err
	}
	plan, err := output.BuildPlanForDiscoveredInvocation(gitCtx, discoveryResult.Invocation)
	if err != nil {
		return StartupSinkPickerContext{}, err
	}
	if err := output.ValidateRawPlan(emitCfg, plan); err != nil {
		return StartupSinkPickerContext{}, err
	}
	report, err := output.BuildReportForPlan(gitCtx, plan, output.ReportOptions{
		IncludeTreeMetadata: NeedsTreeRender(renderCfg),
		Notices:             discovery.DedupePreserveOrder(discoveryResult.Notices),
	})
	if err != nil {
		return StartupSinkPickerContext{}, err
	}
	return StartupSinkPickerContext{
		Config:    cfg,
		Emit:      emitCfg,
		Render:    renderCfg,
		Git:       gitCtx,
		Discovery: discoveryResult,
		Plan:      plan,
		Report:    report,
	}, nil
}

func measureOutputForSinkMenu(plan output.Plan, emitCfg output.EmitConfig) sinkPayloadMeasurement {
	preview, err := renderSinkOutputTextPreview(plan, emitCfg, output.BundleThreshold+1)
	if err != nil {
		return sinkPayloadMeasurement{WouldBundle: true, Err: err}
	}
	bytes := int64(len(preview.Body))
	return sinkPayloadMeasurement{
		Bytes:       bytes,
		WouldBundle: preview.Truncated || bytes >= output.BundleThreshold,
	}
}

func pickOutputSink(ctx StartupSinkPickerContext, measurement sinkPayloadMeasurement) ([]string, bool, error) {
	return pickOutputSinkWithEscHint(ctx, measurement, "")
}

func pickOutputSinkWithEscHint(ctx StartupSinkPickerContext, measurement sinkPayloadMeasurement, escHint string) ([]string, bool, error) {
	choices := startupSinkChoicesSmall
	if measurement.WouldBundle {
		choices = startupSinkChoicesLarge
	}
	lines, index := startupSinkChoiceLines(choices)
	files, err := PrepareStartupSinkPreviewFiles(ctx)
	if err != nil {
		return nil, false, err
	}
	defer files.Cleanup()

	bin, err := discovery.FuzzyResolverBinary()
	if err != nil {
		return nil, false, err
	}
	result, err := picker.Run(bin, themedFzfRequest(picker.Request{
		Prompt:         "output> ",
		WithNth:        "1,3",
		Nth:            "1,3",
		Header:         startupSinkPickerHeaderWithEscHint(escHint),
		PreviewCommand: files.PreviewCommand,
		PreviewWindow:  picker.DefaultPreviewWindow,
		Bindings:       []string{files.ToggleBinding},
		NoSort:         true,
		Exact:          true,
		Lines:          lines,
	}))
	if errors.Is(err, picker.ErrSelectionCancelled) {
		return nil, true, discovery.ErrSelectionCancelled
	}
	if err != nil {
		return nil, true, err
	}
	if len(result.Matches) == 0 {
		return nil, true, discovery.ErrSelectionCancelled
	}
	choice, ok := index[result.Matches[0]]
	if !ok {
		return nil, true, discovery.ErrSelectionCancelled
	}
	return append([]string(nil), choice.Args...), true, nil
}

func startupSinkChoiceLines(choices []startupSinkChoice) ([]string, map[string]startupSinkChoice) {
	lines := make([]string, 0, len(choices))
	index := make(map[string]startupSinkChoice, len(choices))
	labelWidth := 0
	for _, choice := range choices {
		if len(choice.Label) > labelWidth {
			labelWidth = len(choice.Label)
		}
	}
	for _, choice := range choices {
		label := fmt.Sprintf("%-*s", labelWidth, choice.Label)
		lines = append(lines, strings.Join([]string{label, choice.Key, choice.Description}, "\t"))
		index[choice.Key] = choice
	}
	return lines, index
}

func startupSinkPickerHeader() string {
	return startupSinkPickerHeaderWithEscHint("")
}

func startupSinkPickerHeaderWithEscHint(escHint string) string {
	return discovery.PickerHeader(
		"Pick where the output should go.",
		"Preview defaults to output text.",
		"[Ctrl-T] toggle output/tree preview",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

func PrepareStartupSinkPreviewFiles(ctx StartupSinkPickerContext) (startupSinkPreviewFiles, error) {
	tmpdir, err := os.MkdirTemp("", "catclip-sink-preview-*")
	if err != nil {
		return startupSinkPreviewFiles{}, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpdir)
	}
	fail := func(err error) (startupSinkPreviewFiles, error) {
		cleanup()
		return startupSinkPreviewFiles{}, err
	}

	outputPreview, err := renderSinkPreviewWithMode(ctx, sinkPreviewModeOutputText, output.PreviewByteLimit)
	if err != nil {
		return fail(err)
	}
	treePreview, err := renderSinkPreviewWithMode(ctx, sinkPreviewModeTreeReport, output.PreviewByteLimit)
	if err != nil {
		return fail(err)
	}

	outputPath := filepath.Join(tmpdir, "output.txt")
	treePath := filepath.Join(tmpdir, "tree.txt")
	modePath := filepath.Join(tmpdir, "mode")
	if err := os.WriteFile(outputPath, formatSinkPreview(outputPreview), 0o600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(treePath, formatSinkPreview(treePreview), 0o600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(modePath, []byte("output\n"), 0o600); err != nil {
		return fail(err)
	}

	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		return fail(fmt.Errorf("failed to locate catclip executable"))
	}
	selfQuoted := discovery.ShellQuoteArg(self)

	previewCmd := strings.Join([]string{
		selfQuoted,
		"--internal-sink-preview",
		discovery.ShellQuoteArg(modePath),
		discovery.ShellQuoteArg(outputPath),
		discovery.ShellQuoteArg(treePath),
	}, " ")

	toggleCmd := strings.Join([]string{
		selfQuoted,
		"--internal-sink-toggle",
		discovery.ShellQuoteArg(modePath),
	}, " ")

	return startupSinkPreviewFiles{
		PreviewCommand: previewCmd,
		ToggleBinding:  startupSinkPreviewToggleBinding(toggleCmd),
		Cleanup:        cleanup,
	}, nil
}

func startupSinkPreviewToggleBinding(toggleCommand string) string {
	action := "execute-silent(" + toggleCommand + ")+refresh-preview"
	// Key choice constraints:
	//   `?`     — requires shift+/; users intuit `/` (less/vim/fzf
	//             filter), shift requirement isn't discoverable.
	//   `ctrl-/`— failed earlier macOS dogfood.
	//   `ctrl-p`/`ctrl-n` — fzf's input-history bindings; rebinding
	//             would break expected history navigation.
	//   `ctrl-a`— catclip's "toggle all" in multi-select pickers.
	// `ctrl-t` is unbound by fzf's defaults, single modifier+letter,
	// "t" reads as "toggle." Safe and discoverable via the header hint.
	return "ctrl-t:" + action
}

func renderSinkPreviewWithMode(ctx StartupSinkPickerContext, mode sinkPreviewMode, limit int64) (sinkPreview, error) {
	switch mode {
	case sinkPreviewModeTreeReport:
		return renderSinkTreeReportPreview(ctx, limit)
	default:
		return renderSinkOutputTextPreview(ctx.Plan, ctx.Emit, limit)
	}
}

func renderSinkOutputTextPreview(plan output.Plan, emitCfg output.EmitConfig, limit int64) (sinkPreview, error) {
	// The output-text preview shows the exact bytes the chosen sink will
	// emit (file wrappers, line numbers, paths, raw bodies, diffs — all
	// shape choices the user made via flags). Syntax highlighting is
	// applied to file bodies inside <file>...</file> wrappers for
	// readability; the wrappers themselves stay verbatim. Modes without
	// wrappers (--raw, --paths) pass through unhighlighted, matching the
	// emit shape exactly.
	//
	// The byte limit is applied to the EMIT bytes (what the sink would
	// actually paste). Highlighting adds ANSI escapes on top; the final
	// preview pane bytes may exceed the limit, but the truncation
	// decision still reflects the raw emit size.
	var buf bytes.Buffer
	w := output.NewPreviewCapWriter(&buf, context.Background(), limit)
	if err := output.WriteOutputPlanPayloadWithoutPrefetch(w, emitCfg, plan); err != nil && !errors.Is(err, output.ErrPreviewLimitReached) {
		return sinkPreview{}, err
	}
	rawSize := int64(buf.Len())
	highlighted := highlightFileBlocksForSinkPreview(buf.Bytes())
	return sinkPreview{
		Mode:           sinkPreviewModeOutputText,
		Body:           highlighted,
		Truncated:      w.Truncated(),
		FullBytesKnown: !w.Truncated(),
		FullBytes:      rawSize,
	}, nil
}

// highlightFileBlocksForSinkPreview scans an emit-shape buffer for
// <file path="..."> ... </file> blocks and applies chroma syntax
// highlighting to each body. Wrappers stay verbatim. Bytes outside
// any <file>...</file> block (e.g., the leading bytes of a --raw or
// --paths emit) also pass through unchanged. Line-numbered bodies
// (--lines mode) are detected and skipped to avoid chroma being
// confused by the leading "  ##\t" prefixes.
func highlightFileBlocksForSinkPreview(raw []byte) []byte {
	return highlightFileBlocksForSinkPreviewMatches(raw, "")
}

// highlightFileBlocksForSinkPreviewMatches is the snippet-boundary variant of
// highlightFileBlocksForSinkPreview. The file body keeps its Chroma syntax
// colors, while only text matching matchPattern receives the reverse-video
// match emphasis used by --contains previews. The <file> wrappers remain plain.
func highlightFileBlocksForSinkPreviewMatches(raw []byte, matchPattern string) []byte {
	var out bytes.Buffer
	out.Grow(len(raw) + len(raw)/4) // chroma adds ~25% in ANSI codes
	opts := treeRenderOptions{PreviewTheme: "fzf-dark"}
	rest := raw
	openTag := []byte("<file path=\"")
	closeTag := []byte("</file>")
	for len(rest) > 0 {
		idx := bytes.Index(rest, openTag)
		if idx < 0 {
			out.Write(rest)
			break
		}
		out.Write(rest[:idx])
		rest = rest[idx:]

		// Parse the opening tag up to '>'.
		tagEnd := bytes.IndexByte(rest, '>')
		if tagEnd < 0 {
			out.Write(rest)
			break
		}
		out.Write(rest[:tagEnd+1])
		path := sinkPreviewExtractTagPath(rest[:tagEnd+1])
		rest = rest[tagEnd+1:]

		closeIdx := bytes.Index(rest, closeTag)
		if closeIdx < 0 {
			// Unmatched opener (e.g., truncated mid-body) — pass through.
			out.Write(rest)
			break
		}
		body := rest[:closeIdx]
		rest = rest[closeIdx:]

		if path == "" || sinkPreviewBodyHasLineNumbers(body) {
			out.Write(body)
		} else {
			out.WriteString(renderpkg.HighlightFilePreviewWithMatches(path, string(body), matchPattern, opts))
		}

		out.Write(rest[:len(closeTag)])
		rest = rest[len(closeTag):]
	}
	result := make([]byte, out.Len())
	copy(result, out.Bytes())
	return result
}

func sinkPreviewExtractTagPath(tag []byte) string {
	const key = `path="`
	idx := bytes.Index(tag, []byte(key))
	if idx < 0 {
		return ""
	}
	start := idx + len(key)
	end := bytes.IndexByte(tag[start:], '"')
	if end < 0 {
		return ""
	}
	return string(tag[start : start+end])
}

// sinkPreviewBodyHasLineNumbers reports whether the body starts with the
// "  ##\t" prefix that --lines emits. The check is on the first line
// only; --lines applies the prefix to every line uniformly so the first
// line is a reliable signal.
func sinkPreviewBodyHasLineNumbers(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	i := 0
	if body[0] == '\n' {
		i = 1 // skip the newline immediately after the open tag
	}
	hasDigit := false
	for i < len(body) {
		c := body[i]
		if c == ' ' {
			i++
			continue
		}
		if c >= '0' && c <= '9' {
			hasDigit = true
			i++
			continue
		}
		return hasDigit && c == '\t'
	}
	return false
}

func renderSinkTreeReportPreview(ctx StartupSinkPickerContext, limit int64) (sinkPreview, error) {
	var buf bytes.Buffer
	w := output.NewPreviewCapWriter(&buf, context.Background(), limit)
	err := RenderPreview(ctx.Render, ctx.Git, ctx.Plan, ctx.Report, w, w, platform.ANSIPalette())
	if err != nil && !errors.Is(err, output.ErrPreviewLimitReached) {
		return sinkPreview{}, err
	}
	body := append([]byte(nil), buf.Bytes()...)
	return sinkPreview{
		Mode:           sinkPreviewModeTreeReport,
		Body:           body,
		Truncated:      w.Truncated(),
		FullBytesKnown: !w.Truncated(),
		FullBytes:      int64(len(body)),
	}, nil
}

func formatSinkPreview(preview sinkPreview) []byte {
	out := make([]byte, 0, len(preview.Body)+96)
	if preview.Mode == sinkPreviewModeTreeReport {
		out = append(out, "[preview mode: tree/report - Ctrl-T switches to output text]\n\n"...)
	}
	out = append(out, preview.Body...)
	if preview.Truncated {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, "\n[preview truncated at 128 KiB]\n"...)
		if preview.FullBytesKnown {
			out = append(out, fmt.Sprintf("[full output: %s]\n", formatByteCount(preview.FullBytes))...)
		} else {
			out = append(out, "[full output: >=128 KiB]\n"...)
		}
	}
	return out
}
