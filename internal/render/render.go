package render

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

const (
	defaultChromaStyle  = "xcode"
	fzfDarkPreviewStyle = "xcode-dark"
	previewThemeFzfDark = "fzf-dark"
)

const (
	previewMatchStart = "\033[7m"
	previewMatchEnd   = "\033[27m"
)

// RenderDocument renders a tree or file-preview document to the target writer.
func RenderDocument(w io.Writer, doc Document, opts RenderOptions, colors Palette) error {
	switch doc.Mode {
	case DocumentModeFile:
		if err := renderFilePreview(w, doc.File, opts, colors); err != nil {
			return err
		}
	default:
		if len(doc.Entries) == 0 && doc.Target != nil {
			if err := renderEmptyTarget(w, doc.Target, opts, colors); err != nil {
				return err
			}
		} else if err := renderEntries(w, doc.Entries, doc.EntriesSorted, opts, colors); err != nil {
			return err
		}
	}

	if opts.ShowSummary {
		return RenderSummarySection(w, doc.Summary, opts, colors)
	}
	return nil
}

// RenderSummarySection renders only the summary footer for a document.
func RenderSummarySection(w io.Writer, summary *DocumentSummary, opts RenderOptions, colors Palette) error {
	if summary == nil {
		return nil
	}

	fileWord := summary.FileWord
	if fileWord == "" {
		fileWord = fileWordForCount(summary.Count)
	}
	if _, err := fmt.Fprintf(w, "\n  %s%-8s%s %s%d %s%s\n", colors.Bold, "Count:", colors.Reset, colors.Value, summary.Count, fileWord, colors.Reset); err != nil {
		return err
	}

	sizeLabel := summary.HumanSize
	if sizeLabel == "" {
		switch {
		case summary.Bytes > 0:
			sizeLabel = formatByteCount(summary.Bytes)
		default:
			sizeLabel = "0B"
		}
	}
	if _, err := fmt.Fprintf(w, "  %s%-8s%s %s%s%s\n", colors.Bold, "Size:", colors.Reset, colors.Value, sizeLabel, colors.Reset); err != nil {
		return err
	}

	if opts.ShowTokens {
		if _, err := fmt.Fprintf(w, "  %s%-8s%s %s~%d%s\n", colors.Bold, "Tokens:", colors.Reset, colors.Value, summary.Tokens, colors.Reset); err != nil {
			return err
		}
	}
	return nil
}

// AllowedByIncludeDirectoryLabel reports whether a directory label should
// inherit the include-allowed color for an ignored subtree admitted by
// explicit --include.
func AllowedByIncludeDirectoryLabel(entry DocumentEntry, relDir string) bool {
	if !entry.AllowedByInclude {
		return false
	}
	targetRoot := normalizeRelPath(entry.TargetRoot)
	if targetRoot != "" && targetRoot != "." && targetRoot != entry.Path {
		if relDir == targetRoot || strings.HasPrefix(relDir, targetRoot+"/") {
			return true
		}
	}
	return false
}

func renderEmptyTarget(w io.Writer, target *DocumentTarget, opts RenderOptions, colors Palette) error {
	label := renderTargetLabel(target, opts)
	if label != "" {
		if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Dir, label, colors.Reset); err != nil {
			return err
		}
	}

	message := "no previewable text files"
	if target.Kind == TargetKindFile && target.State == TargetStateNonText {
		message = "not a previewable text file"
	}

	_, err := fmt.Fprintf(w, "%s└── %s%s%s\n", colors.Tree, colors.Dim, message, colors.Reset)
	return err
}

func renderTargetLabel(target *DocumentTarget, opts RenderOptions) string {
	if target == nil {
		return ""
	}

	rel := normalizeRelPath(target.Path)
	if rel == "" {
		return ""
	}

	label := rel
	if opts.Bare && rel != "." {
		label = path.Base(rel)
	}
	if target.Kind == TargetKindDir && !strings.HasSuffix(label, "/") {
		label += "/"
	}
	return label
}

func renderEntries(w io.Writer, entries []DocumentEntry, entriesSorted bool, opts RenderOptions, colors Palette) error {
	if !entriesSorted {
		entries = SortedEntries(entries)
	}
	lastParts := []string{}
	lineCount := 0
	landmarks := map[string]bool{}
	if !opts.Bare {
		landmarks = detectLandmarks(entries)
	}
	trimPrefix := ""
	if opts.Bare {
		trimPrefix = bareTrimPrefix(entries)
	}
	for _, entry := range entries {
		relPath := normalizeRelPath(entry.Path)
		if trimPrefix != "" && strings.HasPrefix(relPath, trimPrefix+"/") {
			relPath = strings.TrimPrefix(relPath, trimPrefix+"/")
		}
		parts := strings.Split(relPath, "/")
		if len(parts) == 0 || parts[0] == "" || parts[0] == "." {
			continue
		}

		fileIndex := len(parts) - 1
		common := 0
		for common < fileIndex && common < len(lastParts) && lastParts[common] == parts[common] {
			common++
		}

		accum := ""
		for i := range fileIndex {
			if accum == "" {
				accum = parts[i]
			} else {
				accum += "/" + parts[i]
			}
			if i < common {
				continue
			}

			prefix := treeIndent(i, colors)
			label := parts[i] + "/"
			targetHint := !opts.Bare && shouldShowTargetPathHint(entry.TargetRoot, accum)
			if !opts.Bare && i > 0 && (targetHint || landmarks[accum] || lineCount >= 24) {
				label += " " + colors.Dim + "(" + accum + "/)" + colors.Reset
				lineCount = 0
			}
			dirColor := colors.Dir
			if AllowedByIncludeDirectoryLabel(entry, accum) {
				dirColor = colors.Err
			}
			if _, err := fmt.Fprintf(w, "%s%s%s%s\n", prefix, dirColor, label, colors.Reset); err != nil {
				return err
			}
			lineCount++
		}

		filePrefix := treeIndent(fileIndex, colors)
		if fileIndex == 0 {
			filePrefix = colors.Tree + "├── " + colors.Reset
		}

		nameColor := ""
		nameReset := ""
		if entry.AllowedByInclude {
			nameColor = colors.Err
			nameReset = colors.Reset
		}

		fileLine := filePrefix + nameColor + parts[fileIndex] + nameReset
		if opts.ShowSizes && entry.Size != nil {
			sizeLabel := formatInlineSize(*entry.Size)
			if entry.AllowedByInclude {
				fileLine += " " + colors.Err + "(" + sizeLabel + ")" + colors.Reset
			} else {
				fileLine += " " + styleSize(sizeLabel, *entry.Size, colors)
			}
		}
		if opts.ShowGitStatus && entry.GitStatus != "" {
			fileLine += " " + styleStatus(entry.GitStatus, colors)
		}
		if opts.ShowModeTags && entry.ModeTag != "" {
			fileLine += " " + colors.Git + "[" + entry.ModeTag + "]" + colors.Reset
		}
		if _, err := fmt.Fprintln(w, fileLine); err != nil {
			return err
		}
		lineCount++
		lastParts = parts[:fileIndex]
	}
	return nil
}

func renderFilePreview(w io.Writer, file *FilePreview, opts RenderOptions, colors Palette) error {
	if file == nil {
		return nil
	}
	if file.Path != "" && file.Path != "." {
		if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Bold, file.Path, colors.Reset); err != nil {
			return err
		}
	}

	content := file.Content
	rawLines := splitPreviewLines(file.Content)
	if shouldHighlightFilePreview(colors) {
		highlightPath := file.HighlightPath
		if strings.TrimSpace(highlightPath) == "" {
			highlightPath = file.Path
		}
		content = highlightFilePreview(highlightPath, file.Content, opts)
	}

	lines := splitPreviewLines(content)
	lines = overlayPreviewMatchHighlights(lines, rawLines, file.MatchPattern)
	lines = overlayPreviewFocusLineHighlights(lines, file.FocusLines)
	highlightedLineNumbers := previewHighlightedLineNumbers(rawLines, file.MatchPattern, file.FocusLines)
	truncated := file.Truncated
	if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
		lines = lines[:opts.MaxLines]
		truncated = true
	}

	width := max(len(strconv.Itoa(len(lines))), 1)
	for i, line := range lines {
		lineNo := i + 1
		lineNumber := stylePreviewLineNumber(fmt.Sprintf("%*d", width, lineNo), lineNo, highlightedLineNumbers, opts, colors)
		if _, err := fmt.Fprintf(w, "%s %s│%s %s\n", lineNumber, colors.Tree, colors.Reset, line); err != nil {
			return err
		}
	}

	if truncated {
		_, err := fmt.Fprintf(w, "%s… truncated%s\n", colors.Dim, colors.Reset)
		return err
	}
	return nil
}

func shouldHighlightFilePreview(colors Palette) bool {
	return colors.Reset != "" || colors.Tree != "" || colors.Label != ""
}

func previewLineNumberColor(opts RenderOptions, colors Palette) string {
	if normalizePreviewTheme(opts.PreviewTheme) == previewThemeFzfDark {
		return "\033[37m"
	}
	return colors.Label
}

func stylePreviewLineNumber(text string, lineNo int, highlighted map[int]struct{}, opts RenderOptions, colors Palette) string {
	styled := previewLineNumberColor(opts, colors) + text + colors.Reset
	if _, ok := highlighted[lineNo]; ok {
		return highlightWholeLineANSI(styled)
	}
	return styled
}

// HighlightFilePreview is the exported entry point for applying the same
// chroma syntax highlighting to a string that the file-mode tree renderer
// uses for previewable text. It is used by the sink picker to color file
// bodies inside the emit-shape preview ("show exact bytes the sink will
// emit, but with syntax highlighting on the body"). For internal callers
// inside this package the lowercase shim below stays as the name.
func HighlightFilePreview(relPath, content string, opts RenderOptions) string {
	return highlightFilePreview(relPath, content, opts)
}

func highlightFilePreview(relPath, content string, opts RenderOptions) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	if strings.TrimSpace(relPath) == "diff" {
		return highlightUnifiedDiffPreview(content)
	}

	lexer := lexerForPath(relPath)
	if lexer == nil {
		// No filename-based lexer for this type. Fall back to content analysis,
		// which is content-dependent and therefore NOT cached (every body differs).
		lexer = lexers.Analyse(content)
		if lexer == nil || lexer == lexers.Fallback {
			return content
		}
		lexer = chroma.Coalesce(lexer)
	}

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}

	style := previewChromaStyle(opts)

	var buf bytes.Buffer
	if err := formatters.TTY256.Format(&buf, style, iterator); err != nil {
		return content
	}
	return buf.String()
}

// lexerForPath returns the chroma lexer chroma's filename matching would pick for
// relPath, memoized by file TYPE. lexers.Match globs the filename against every
// registered lexer via path/filepath.Match — ~86% of the sink preview's CPU when
// highlighting hundreds of <file> blocks (e.g. `--snippet 'func' 0` in a Go repo
// emits one block per matched file, and each re-scanned all ~250 lexers).
//
// The selection depends only on the filename's extension (or, for extension-less
// files, the basename), so caching by that key makes the cost O(distinct types)
// instead of O(blocks): 500 .go files do ONE match, not 500. It is exact, not
// approximate — chroma's filename match is deterministic per filename, so every
// file of a type resolves to the same lexer. The cached lexer is already coalesced
// and safe to reuse across Tokenise calls (chroma lexers are stateless analyzers,
// and reuse also avoids recompiling the lexer's regex rules per file). A cached
// nil means "no filename-based lexer for this type"; the caller falls back to
// per-file content analysis.
var (
	lexerCacheMu sync.RWMutex
	lexerCache   = map[string]chroma.Lexer{}
)

func lexerForPath(relPath string) chroma.Lexer {
	key := lexerCacheKey(relPath)
	lexerCacheMu.RLock()
	lexer, ok := lexerCache[key]
	lexerCacheMu.RUnlock()
	if ok {
		return lexer
	}

	lexer = lexers.Match(relPath)
	if lexer == nil {
		lexer = lexers.Get(strings.TrimSpace(relPath))
	}
	if lexer != nil && lexer != lexers.Fallback {
		lexer = chroma.Coalesce(lexer)
	} else {
		lexer = nil
	}

	lexerCacheMu.Lock()
	lexerCache[key] = lexer
	lexerCacheMu.Unlock()
	return lexer
}

// lexerCacheKey reduces relPath to its chroma-selection-relevant identity: the
// lowercased extension when present (the common case — globs are almost all
// "*.ext"), else the basename (for extension-less names chroma matches whole,
// like "Makefile" or "Dockerfile"). relPath is forward-slash, so path is correct.
func lexerCacheKey(relPath string) string {
	base := path.Base(strings.TrimSpace(relPath))
	if ext := path.Ext(base); ext != "" {
		return "ext:" + strings.ToLower(ext)
	}
	return "name:" + base
}

func chromaStyleForPreview(opts RenderOptions) string {
	if normalizePreviewTheme(opts.PreviewTheme) == previewThemeFzfDark {
		return fzfDarkPreviewStyle
	}
	return defaultChromaStyle
}

func previewChromaStyle(opts RenderOptions) *chroma.Style {
	style := styles.Get(chromaStyleForPreview(opts))
	if style == nil {
		style = styles.Fallback
	}
	if normalizePreviewTheme(opts.PreviewTheme) == previewThemeFzfDark {
		return clearChromaBackground(style)
	}
	return style
}

func normalizePreviewTheme(theme string) string {
	return strings.ToLower(strings.TrimSpace(theme))
}

func clearChromaBackground(style *chroma.Style) *chroma.Style {
	if style == nil {
		return nil
	}
	builder := style.Builder()
	bg := builder.Get(chroma.Background)
	bg.Background = 0
	bg.NoInherit = true
	builder.AddEntry(chroma.Background, bg)
	cleared, err := builder.Build()
	if err != nil {
		return style
	}
	return cleared
}

func highlightUnifiedDiffPreview(content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 {
		return content
	}

	var b strings.Builder
	for _, line := range lines {
		style := ""
		switch {
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "), strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			style = "\x1b[1;36m"
		case strings.HasPrefix(line, "@@"):
			style = "\x1b[35m"
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ "):
			style = "\x1b[32m"
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- "):
			style = "\x1b[31m"
		}
		if style == "" {
			b.WriteString(line)
			continue
		}
		b.WriteString(style)
		b.WriteString(line)
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func splitPreviewLines(content string) []string {
	if content == "" {
		return nil
	}

	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func overlayPreviewMatchHighlights(lines, rawLines []string, pattern string) []string {
	if len(lines) == 0 || len(rawLines) == 0 || strings.TrimSpace(pattern) == "" {
		return lines
	}

	compiledPattern := pattern
	if isSmartCaseInsensitive(pattern) {
		compiledPattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(compiledPattern)
	if err != nil {
		return lines
	}

	limit := min(len(rawLines), len(lines))
	for i := 0; i < limit; i++ {
		lines[i] = highlightMatchLineANSI(lines[i], rawLines[i], re)
	}
	return lines
}

func overlayPreviewFocusLineHighlights(lines []string, focusLines []int) []string {
	if len(lines) == 0 || len(focusLines) == 0 {
		return lines
	}

	for _, lineNo := range focusLines {
		if lineNo < 1 || lineNo > len(lines) {
			continue
		}
		lines[lineNo-1] = highlightWholeLineANSI(lines[lineNo-1])
	}
	return lines
}

func previewHighlightedLineNumbers(rawLines []string, pattern string, focusLines []int) map[int]struct{} {
	highlighted := make(map[int]struct{}, len(focusLines))
	for _, lineNo := range focusLines {
		if lineNo < 1 {
			continue
		}
		highlighted[lineNo] = struct{}{}
	}
	if len(rawLines) == 0 || strings.TrimSpace(pattern) == "" {
		return highlighted
	}

	compiledPattern := pattern
	if isSmartCaseInsensitive(pattern) {
		compiledPattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(compiledPattern)
	if err != nil {
		return highlighted
	}
	for i, line := range rawLines {
		if re.MatchString(line) {
			highlighted[i+1] = struct{}{}
		}
	}
	return highlighted
}

func highlightWholeLineANSI(ansiLine string) string {
	if ansiLine == "" {
		return ansiLine
	}

	var b strings.Builder
	b.Grow(len(ansiLine) + len(previewMatchStart) + len(previewMatchEnd))
	b.WriteString(previewMatchStart)

	for i := 0; i < len(ansiLine); {
		if ansiLine[i] == '\x1b' {
			end := ansiSequenceEnd(ansiLine, i)
			seq := ansiLine[i:end]
			b.WriteString(seq)
			if isANSIResetSequence(seq) {
				b.WriteString(previewMatchStart)
			}
			i = end
			continue
		}

		_, size := utf8.DecodeRuneInString(ansiLine[i:])
		if size <= 0 {
			break
		}
		b.WriteString(ansiLine[i : i+size])
		i += size
	}

	b.WriteString(previewMatchEnd)
	return b.String()
}

func highlightMatchLineANSI(ansiLine, rawLine string, re *regexp.Regexp) string {
	if ansiLine == "" || rawLine == "" || re == nil {
		return ansiLine
	}

	spans := re.FindAllStringIndex(rawLine, -1)
	if len(spans) == 0 {
		return ansiLine
	}

	filtered := spans[:0]
	for _, span := range spans {
		if len(span) != 2 || span[0] >= span[1] {
			continue
		}
		filtered = append(filtered, span)
	}
	if len(filtered) == 0 {
		return ansiLine
	}

	var b strings.Builder
	b.Grow(len(ansiLine) + len(filtered)*(len(previewMatchStart)+len(previewMatchEnd)))

	rawPos := 0
	spanIndex := 0
	inMatch := false

	for i := 0; i < len(ansiLine); {
		if ansiLine[i] == '\x1b' {
			end := ansiSequenceEnd(ansiLine, i)
			seq := ansiLine[i:end]
			b.WriteString(seq)
			if inMatch && isANSIResetSequence(seq) {
				b.WriteString(previewMatchStart)
			}
			i = end
			continue
		}

		for spanIndex < len(filtered) && rawPos >= filtered[spanIndex][1] {
			if inMatch {
				b.WriteString(previewMatchEnd)
				inMatch = false
			}
			spanIndex++
		}
		if spanIndex < len(filtered) && rawPos == filtered[spanIndex][0] && !inMatch {
			b.WriteString(previewMatchStart)
			inMatch = true
		}

		_, ansiSize := utf8.DecodeRuneInString(ansiLine[i:])
		if ansiSize <= 0 {
			break
		}
		if rawPos < len(rawLine) {
			_, rawSize := utf8.DecodeRuneInString(rawLine[rawPos:])
			if rawSize > 0 {
				rawPos += rawSize
			}
		}
		b.WriteString(ansiLine[i : i+ansiSize])
		i += ansiSize

		if inMatch && spanIndex < len(filtered) && rawPos >= filtered[spanIndex][1] {
			b.WriteString(previewMatchEnd)
			inMatch = false
			spanIndex++
		}
	}

	if inMatch {
		b.WriteString(previewMatchEnd)
	}
	return b.String()
}

func ansiSequenceEnd(text string, start int) int {
	end := start + 1
	if end >= len(text) || text[end] != '[' {
		return end
	}
	end++
	for end < len(text) {
		b := text[end]
		end++
		if b >= '@' && b <= '~' {
			return end
		}
	}
	return len(text)
}

func isANSIResetSequence(seq string) bool {
	return seq == "\x1b[0m" || seq == "\x1b[m"
}

func detectLandmarks(entries []DocumentEntry) map[string]bool {
	nameCounts := map[string]map[string]struct{}{}
	for _, entry := range entries {
		dir := path.Dir(entry.Path)
		if dir == "." {
			continue
		}
		accum := ""
		for segment := range strings.SplitSeq(dir, "/") {
			if segment == "" || segment == "." {
				continue
			}
			if accum == "" {
				accum = segment
			} else {
				accum = accum + "/" + segment
			}
			if nameCounts[segment] == nil {
				nameCounts[segment] = map[string]struct{}{}
			}
			nameCounts[segment][accum] = struct{}{}
		}
	}

	landmarks := map[string]bool{}
	for _, paths := range nameCounts {
		if len(paths) <= 1 {
			continue
		}
		for full := range paths {
			landmarks[full] = true
		}
	}
	return landmarks
}

func bareTrimPrefix(entries []DocumentEntry) string {
	if len(entries) == 0 {
		return ""
	}

	targetRoot := normalizeRelPath(entries[0].TargetRoot)
	if targetRoot == "" || targetRoot == "." {
		return ""
	}
	for _, entry := range entries[1:] {
		if normalizeRelPath(entry.TargetRoot) != targetRoot {
			return ""
		}
	}

	parent := normalizeRelPath(path.Dir(targetRoot))
	if parent == "" || parent == "." {
		return ""
	}
	prefix := normalizeRelPath(path.Dir(parent))
	if prefix == "" || prefix == "." {
		return ""
	}
	return prefix
}

func shouldShowTargetPathHint(targetRoot, accum string) bool {
	targetRoot = normalizeRelPath(targetRoot)
	if targetRoot == "." || targetRoot == "" {
		return false
	}
	if !strings.Contains(targetRoot, "/") {
		return false
	}
	return targetRoot == accum
}

func treeIndent(depth int, colors Palette) string {
	if depth <= 0 {
		return ""
	}
	return strings.Repeat(colors.Tree+"│   "+colors.Reset, depth) + colors.Tree + "├── " + colors.Reset
}

func styleSize(label string, size int64, colors Palette) string {
	switch {
	case size < 40000:
		return colors.Dim + "(" + label + ")" + colors.Reset
	case size < 200000:
		return colors.Warn + "(" + label + ")" + colors.Reset
	default:
		return colors.Err + "(" + label + ")" + colors.Reset
	}
}

func styleStatus(status string, colors Palette) string {
	switch status {
	case "SM":
		return colors.Warn + "[SM]" + colors.Reset
	case "M":
		return colors.Warn + "[M]" + colors.Reset
	case "S":
		return colors.OK + "[S]" + colors.Reset
	case "?":
		return colors.Git + "[?]" + colors.Reset
	default:
		return "[" + status + "]"
	}
}

func formatInlineSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%dB", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	}
}

func formatByteCount(totalBytes int64) string {
	switch {
	case totalBytes < 1024:
		return fmt.Sprintf("%dB", totalBytes)
	case totalBytes < 1024*1024:
		return fmt.Sprintf("%.2fKB", float64(totalBytes)/1024)
	case totalBytes < 1024*1024*1024:
		return fmt.Sprintf("%.2fMB", float64(totalBytes)/(1024*1024))
	default:
		return fmt.Sprintf("%.2fGB", float64(totalBytes)/(1024*1024*1024))
	}
}

func isSmartCaseInsensitive(pattern string) bool {
	for _, r := range pattern {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}
