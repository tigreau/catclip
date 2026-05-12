package tree

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
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
		bare         bool
		showSizes    bool
		showGit      bool
		showSum      bool
		showTok      bool
		colorMode    string
		previewTheme string
		maxLines     int
		showVer      bool
		inputFile    string
	)

	fs := flag.NewFlagSet("catclip-tree", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&bare, "bare", false, "render a minimal tree")
	fs.BoolVar(&showSizes, "sizes", false, "show file sizes if present in the payload")
	fs.BoolVar(&showGit, "git", false, "show git status badges if present in the payload")
	fs.BoolVar(&showSum, "summary", false, "show summary section if present in the payload")
	fs.BoolVar(&showTok, "tokens", false, "show token estimate in the summary")
	fs.StringVar(&colorMode, "color", "auto", "color mode: auto, always, or never")
	fs.StringVar(&previewTheme, "preview-theme", "", "internal preview theme")
	fs.IntVar(&maxLines, "max-lines", 0, "maximum rendered lines for file preview mode (0 = unlimited)")
	fs.BoolVar(&showVer, "version", false, "show version")
	fs.StringVar(&inputFile, "input-file", "", "read payload from PATH instead of stdin")
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

	opts := DefaultRenderOptions()
	opts.MaxLines = maxLines
	opts.PreviewTheme = normalizePreviewTheme(previewTheme)
	switch opts.PreviewTheme {
	case "", previewThemeFzfDark:
	default:
		return usageErrorf("Error: invalid preview theme %s\n  Use one of: %s", singleQuoted(previewTheme), singleQuoted(previewThemeFzfDark))
	}
	if bare {
		opts.Bare = true
		opts.ShowSizes = false
		opts.ShowGitStatus = false
		opts.ShowSummary = false
		opts.ShowTokens = false
	}
	if showSizes {
		opts.ShowSizes = true
	}
	if showGit {
		opts.ShowGitStatus = true
	}
	if showSum {
		opts.ShowSummary = true
	}
	if showTok {
		opts.ShowSummary = true
		opts.ShowTokens = true
	}

	colors, err := resolvePalette(colorMode, stdout, cfg.ResolvePalette)
	if err != nil {
		return err
	}

	payload := stdin
	if inputFile != "" {
		f, err := os.Open(inputFile)
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
	b.WriteString("  --bare            Render a minimal tree\n")
	b.WriteString("  --sizes           Show file sizes if present in the payload\n")
	b.WriteString("  --git             Show git status badges if present in the payload\n")
	b.WriteString("  --summary         Show summary section if present in the payload\n")
	b.WriteString("  --tokens          Show token estimate in the summary\n")
	b.WriteString("  --color MODE      auto, always, or never\n")
	b.WriteString("  --max-lines N     Maximum lines in file preview mode (0 = unlimited)\n")
	b.WriteString("  --input-file PATH Read payload from PATH instead of stdin\n")
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
