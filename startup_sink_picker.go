package catclip

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/picker"
	treepkg "github.com/tigreau/catclip/internal/tree"
)

const sinkPreviewDirectPasteByteLimit = 128 * 1024

var errSinkPreviewLimitReached = errors.New("sink preview limit reached")

type startupPreparedOutputState struct {
	Git       gitContext
	Discovery DiscoveryResult
	Plan      outputPlan
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

type startupSinkPickerContext struct {
	Config    parsedCommand
	Emit      emitConfig
	Render    renderConfig
	Git       gitContext
	Discovery DiscoveryResult
	Plan      outputPlan
	Report    outputReport
}

type startupSinkPreviewFiles struct {
	PreviewCommand string
	ToggleBinding  string
	Cleanup        func()
}

type limitedBufferWriter struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
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
		Key:         "headless",
		Label:       "Headless - agent / script contract",
		Description: "Print to stdout with quiet stderr and no prompts.",
		Args:        []string{"--headless"},
	},
}

func maybeResolveStartupSinkPickerArgs(rawArgs []string, result startupPickerResult) (startupPickerResult, error) {
	if !result.UsedFzf || len(result.Args) == 0 || rawArgsSkipOutputSinkPicker(rawArgs) {
		return result, nil
	}

	ctx, err := buildStartupSinkPickerContext(result.Args)
	if err != nil {
		return startupPickerResult{}, err
	}
	measurement := measureOutputForSinkMenu(ctx.Plan, ctx.Emit)
	sinkArgs, usedFzf, err := pickOutputSink(ctx, measurement)
	if err != nil {
		return startupPickerResult{}, err
	}

	out := result
	out.Args = append(append([]string(nil), result.Args...), sinkArgs...)
	out.UsedFzf = out.UsedFzf || usedFzf
	out.ForceResolvedCommand = out.ForceResolvedCommand || (argsContain(sinkArgs, "--headless") && !rawArgsRequestQuiet(rawArgs))
	out.PreparedOutput = &startupPreparedOutputState{
		Git:       ctx.Git,
		Discovery: ctx.Discovery,
		Plan:      ctx.Plan,
	}
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

func buildStartupSinkPickerContext(args []string) (startupSinkPickerContext, error) {
	cfg, err := parseArgsAllowImplicitDot(args)
	if err != nil {
		return startupSinkPickerContext{}, err
	}
	emitCfg := emitConfigFromParsedCommand(cfg)
	renderCfg := renderConfigFromParsedCommand(cfg)
	renderCfg.Preview = true
	gitCtx := detectGitContext(cfg.WorkingDir)
	// Discovery diagnostics from this pass get embedded in the prepared
	// state and replayed by run() after the picker resolves. They must
	// carry the same ANSI codes a non-interactive run would produce; an
	// empty palette here would strip color from every diagnostic
	// (ignored-target errors, target-not-found warnings, etc.) the user
	// later sees on stderr after picker confirmation.
	colors := activeColorPalette()

	discovery, err := discoverInvocation(resolvedInvocationFromParsedCommand(cfg), gitCtx, io.Discard, colors)
	if err != nil {
		return startupSinkPickerContext{}, err
	}
	plan, err := buildOutputPlanForDiscoveredInvocation(gitCtx, discovery.Invocation)
	if err != nil {
		return startupSinkPickerContext{}, err
	}
	if err := validateRawOutputPlan(emitCfg, plan); err != nil {
		return startupSinkPickerContext{}, err
	}
	report, err := buildOutputReportForPlan(renderCfg, gitCtx, plan, dedupePreserveOrder(discovery.Notices))
	if err != nil {
		return startupSinkPickerContext{}, err
	}
	return startupSinkPickerContext{
		Config:    cfg,
		Emit:      emitCfg,
		Render:    renderCfg,
		Git:       gitCtx,
		Discovery: discovery,
		Plan:      plan,
		Report:    report,
	}, nil
}

func measureOutputForSinkMenu(plan outputPlan, emitCfg emitConfig) sinkPayloadMeasurement {
	preview, err := renderSinkOutputTextPreview(plan, emitCfg, bundleThreshold+1)
	if err != nil {
		return sinkPayloadMeasurement{WouldBundle: true, Err: err}
	}
	bytes := int64(len(preview.Body))
	return sinkPayloadMeasurement{
		Bytes:       bytes,
		WouldBundle: preview.Truncated || bytes >= bundleThreshold,
	}
}

func pickOutputSink(ctx startupSinkPickerContext, measurement sinkPayloadMeasurement) ([]string, bool, error) {
	return pickOutputSinkWithEscHint(ctx, measurement, "")
}

func pickOutputSinkWithEscHint(ctx startupSinkPickerContext, measurement sinkPayloadMeasurement, escHint string) ([]string, bool, error) {
	choices := startupSinkChoicesSmall
	if measurement.WouldBundle {
		choices = startupSinkChoicesLarge
	}
	lines, index := startupSinkChoiceLines(choices)
	files, err := prepareStartupSinkPreviewFiles(ctx)
	if err != nil {
		return nil, false, err
	}
	defer files.Cleanup()

	bin, err := fuzzyResolverBinary()
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
		return nil, true, errSelectionCancelled
	}
	if err != nil {
		return nil, true, err
	}
	if len(result.Matches) == 0 {
		return nil, true, errSelectionCancelled
	}
	choice, ok := index[result.Matches[0]]
	if !ok {
		return nil, true, errSelectionCancelled
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
	return pickerHeader(
		"Pick where the output should go.",
		"Preview defaults to output text.",
		"[Ctrl-T] toggle output/tree preview",
		fmt.Sprintf("[Up/Down] move  [Enter] confirm  %s", startupEscLabel(escHint)),
	)
}

func prepareStartupSinkPreviewFiles(ctx startupSinkPickerContext) (startupSinkPreviewFiles, error) {
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

	outputPreview, err := renderSinkPreviewWithMode(ctx, sinkPreviewModeOutputText, sinkPreviewDirectPasteByteLimit)
	if err != nil {
		return fail(err)
	}
	treePreview, err := renderSinkPreviewWithMode(ctx, sinkPreviewModeTreeReport, sinkPreviewDirectPasteByteLimit)
	if err != nil {
		return fail(err)
	}

	outputPath := filepath.Join(tmpdir, "output.txt")
	treePath := filepath.Join(tmpdir, "tree.txt")
	modePath := filepath.Join(tmpdir, "mode")
	previewScriptPath := filepath.Join(tmpdir, "preview")
	toggleScriptPath := filepath.Join(tmpdir, "toggle")
	if err := os.WriteFile(outputPath, formatSinkPreview(outputPreview), 0o600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(treePath, formatSinkPreview(treePreview), 0o600); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(modePath, []byte("output\n"), 0o600); err != nil {
		return fail(err)
	}

	previewScript := fmt.Sprintf(`#!/bin/sh
mode="$(cat %s 2>/dev/null)"
case "$mode" in
	tree) cat %s ;;
	*) cat %s ;;
esac
`, shellQuoteArg(modePath), shellQuoteArg(treePath), shellQuoteArg(outputPath))
	if err := os.WriteFile(previewScriptPath, []byte(previewScript), 0o700); err != nil {
		return fail(err)
	}

	toggleScript := fmt.Sprintf(`#!/bin/sh
mode="$(cat %s 2>/dev/null)"
if [ "$mode" = "tree" ]; then
	printf '%%s\n' output > %s
else
	printf '%%s\n' tree > %s
fi
`, shellQuoteArg(modePath), shellQuoteArg(modePath), shellQuoteArg(modePath))
	if err := os.WriteFile(toggleScriptPath, []byte(toggleScript), 0o700); err != nil {
		return fail(err)
	}

	return startupSinkPreviewFiles{
		PreviewCommand: shellQuoteArg(previewScriptPath),
		ToggleBinding:  startupSinkPreviewToggleBinding(toggleScriptPath),
		Cleanup:        cleanup,
	}, nil
}

func startupSinkPreviewToggleBinding(toggleScriptPath string) string {
	action := "execute-silent(" + shellQuoteArg(toggleScriptPath) + ")+refresh-preview"
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

func renderSinkPreviewWithMode(ctx startupSinkPickerContext, mode sinkPreviewMode, limit int64) (sinkPreview, error) {
	switch mode {
	case sinkPreviewModeTreeReport:
		return renderSinkTreeReportPreview(ctx, limit)
	default:
		return renderSinkOutputTextPreview(ctx.Plan, ctx.Emit, limit)
	}
}

func renderSinkOutputTextPreview(plan outputPlan, emitCfg emitConfig, limit int64) (sinkPreview, error) {
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
	w := &limitedBufferWriter{limit: limit}
	if err := writeOutputPlanPayloadWithoutPrefetch(w, emitCfg, plan); err != nil && !errors.Is(err, errSinkPreviewLimitReached) {
		return sinkPreview{}, err
	}
	rawSize := int64(len(w.buf.Bytes()))
	highlighted := highlightFileBlocksForSinkPreview(w.buf.Bytes())
	return sinkPreview{
		Mode:           sinkPreviewModeOutputText,
		Body:           highlighted,
		Truncated:      w.truncated,
		FullBytesKnown: !w.truncated,
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
			out.WriteString(treepkg.HighlightFilePreview(path, string(body), opts))
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

func renderSinkTreeReportPreview(ctx startupSinkPickerContext, limit int64) (sinkPreview, error) {
	w := &limitedBufferWriter{limit: limit}
	err := renderPreview(ctx.Render, ctx.Git, ctx.Plan, ctx.Report, w, w, ansiColorPalette())
	if err != nil && !errors.Is(err, errSinkPreviewLimitReached) {
		return sinkPreview{}, err
	}
	body := append([]byte(nil), w.buf.Bytes()...)
	return sinkPreview{
		Mode:           sinkPreviewModeTreeReport,
		Body:           body,
		Truncated:      w.truncated,
		FullBytesKnown: !w.truncated,
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

func (w *limitedBufferWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if w.limit <= 0 {
		w.truncated = true
		return 0, errSinkPreviewLimitReached
	}
	remaining := w.limit - int64(w.buf.Len())
	if remaining <= 0 {
		w.truncated = true
		return 0, errSinkPreviewLimitReached
	}
	if int64(len(p)) > remaining {
		w.truncated = true
		n, _ := w.buf.Write(p[:int(remaining)])
		return n, errSinkPreviewLimitReached
	}
	return w.buf.Write(p)
}
