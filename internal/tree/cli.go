package tree

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type UsageError struct {
	Message string
}

// Error returns the tree CLI usage error text.
func (e UsageError) Error() string {
	return e.Message
}

// CLIConfig provides the tree CLI with environment-specific helpers supplied by
// the root catclip package.
type CLIConfig struct {
	Version        string
	ResolvePalette func(mode string, w io.Writer) (Palette, error)
}

// RunCLI parses catclip-tree flags, decodes a tree payload from stdin, and
// renders it to stdout.
func RunCLI(args []string, stdin io.Reader, stdout, stderr io.Writer, cfg CLIConfig) error {
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = "dev"
	}

	var (
		shapeTags     bool
		gitBadges     bool
		entrySizes    bool
		summaryFooter bool
		colorMode     string
		previewTheme  string
		maxLines      int
		showVer       bool
		inputFile     string
		inputDir      string
		inputStem     string
	)

	fs := flag.NewFlagSet("catclip-tree", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&shapeTags, "shape-tags", false, "show output-shape tags if present in the payload")
	fs.BoolVar(&gitBadges, "git-badges", false, "show git status badges if present in the payload")
	fs.BoolVar(&entrySizes, "entry-sizes", false, "show per-entry sizes if present in the payload")
	fs.BoolVar(&summaryFooter, "summary-footer", false, "show count + size + token footer if present in the payload")
	fs.StringVar(&colorMode, "color", "auto", "color mode: auto, always, or never")
	fs.StringVar(&previewTheme, "preview-theme", "", "internal preview theme")
	fs.IntVar(&maxLines, "max-lines", 0, "maximum rendered lines for file preview mode (0 = unlimited)")
	fs.BoolVar(&showVer, "version", false, "show version")
	fs.StringVar(&inputFile, "input-file", "", "read payload from PATH instead of stdin")
	fs.StringVar(&inputDir, "input-dir", "", "read payload from --input-dir/--input-stem.json (cross-platform alternative to --input-file for fzf preview commands where {N} substitution would otherwise need shell-quoting tricks)")
	fs.StringVar(&inputStem, "input-stem", "", "basename (without extension) within --input-dir; joined as DIR/STEM.json")
	fs.Usage = func() {
		_, _ = io.WriteString(stderr, helpText(version))
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return UsageError{Message: strings.TrimSpace(err.Error())}
	}
	if showVer {
		_, err := fmt.Fprintf(stdout, "catclip-tree %s\n", version)
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("Error: catclip-tree reads payload from stdin and does not accept path arguments.\n  Example: catclip --internal-tree-payload src | catclip-tree")
	}

	// CLI starts bare; each metadata column is opted in additively. The
	// in-process final render uses DefaultRenderOptions() instead.
	opts := BareRenderOptions()
	opts.MaxLines = maxLines
	opts.PreviewTheme = normalizePreviewTheme(previewTheme)
	switch opts.PreviewTheme {
	case "", previewThemeFzfDark:
	default:
		return usageErrorf("Error: invalid preview theme %s\n  Use one of: %s", singleQuoted(previewTheme), singleQuoted(previewThemeFzfDark))
	}
	if shapeTags {
		opts.ShowModeTags = true
	}
	if gitBadges {
		opts.ShowGitStatus = true
	}
	if entrySizes {
		opts.ShowSizes = true
	}
	if summaryFooter {
		opts.ShowSummary = true
		opts.ShowTokens = true
	}

	colors, err := resolvePalette(colorMode, stdout, cfg.ResolvePalette)
	if err != nil {
		return err
	}

	resolvedInputFile := inputFile
	if resolvedInputFile == "" && inputDir != "" && inputStem != "" {
		// Cross-platform path assembly: keeping --input-dir and --input-stem
		// as separate flags lets fzf preview commands pass {N} substitutions
		// as standalone args, avoiding shell-quoting differences between
		// POSIX sh and Windows cmd.exe. See depth_picker.go for the caller.
		resolvedInputFile = filepath.Join(inputDir, inputStem+".json")
	}

	payload := stdin
	if resolvedInputFile != "" {
		f, err := os.Open(resolvedInputFile)
		if err != nil {
			return err
		}
		defer f.Close()
		payload = f
	}

	doc, err := DecodePayload(payload)
	if err != nil {
		if opts.Bare && errors.Is(err, ErrEmptyPayload) {
			return renderBareEmptyPreview(stdout, colors)
		}
		return err
	}

	return RenderDocument(stdout, doc, opts, colors)
}

func resolvePalette(mode string, w io.Writer, resolver func(string, io.Writer) (Palette, error)) (Palette, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "auto", "always", "never":
	default:
		return Palette{}, usageErrorf("Error: invalid color mode %s\n  Use one of: auto, always, never", singleQuoted(mode))
	}
	if resolver == nil {
		return Palette{}, nil
	}
	return resolver(mode, w)
}

func renderBareEmptyPreview(stdout io.Writer, colors Palette) error {
	if _, err := fmt.Fprintf(stdout, "%sNo previewable text files here.%s\n", colors.Warn, colors.Reset); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "%sDirectory may be empty or contain only binary files.%s\n", colors.Dim, colors.Reset)
	return err
}

func helpText(version string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "catclip-tree v%s — Render catclip preview payloads from stdin\n\n", version)
	b.WriteString("Usage:\n")
	b.WriteString("  catclip --internal-tree-payload src | catclip-tree [options]\n\n")
	b.WriteString("Options:\n")
	b.WriteString("  Default is a bare tree (paths only). Each flag adds one metadata column.\n")
	b.WriteString("  --shape-tags      Show output-shape tags if present in the payload\n")
	b.WriteString("  --git-badges      Show git status badges if present in the payload\n")
	b.WriteString("  --entry-sizes     Show per-entry sizes if present in the payload\n")
	b.WriteString("  --summary-footer  Show count + size + token footer if present in the payload\n")
	b.WriteString("  --color MODE      auto, always, or never\n")
	b.WriteString("  --max-lines N     Maximum lines in file preview mode (0 = unlimited)\n")
	b.WriteString("  --input-file PATH Read payload from PATH instead of stdin\n")
	b.WriteString("  --input-dir DIR   Read payload from DIR/STEM.json (paired with --input-stem)\n")
	b.WriteString("  --input-stem STEM Basename within --input-dir; joined as DIR/STEM.json\n")
	b.WriteString("  --version         Show version\n")
	b.WriteString("  -h, --help        Show this help\n")
	return b.String()
}

func usageErrorf(format string, args ...any) error {
	return UsageError{Message: fmt.Sprintf(format, args...)}
}

func singleQuoted(value string) string {
	return "'" + value + "'"
}
