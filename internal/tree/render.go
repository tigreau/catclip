package tree

import (
	"bytes"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

const defaultChromaStyle = "xcode"

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
		} else if err := renderEntries(w, doc.Entries, opts, colors); err != nil {
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
	if _, err := fmt.Fprintf(w, "\n  %s%-8s%s %s%d %s%s\n", colors.Label, "Count:", colors.Reset, colors.Value, summary.Count, fileWord, colors.Reset); err != nil {
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
	if _, err := fmt.Fprintf(w, "  %s%-8s%s %s%s%s\n", colors.Label, "Size:", colors.Reset, colors.Value, sizeLabel, colors.Reset); err != nil {
		return err
	}

	if opts.ShowTokens {
		if _, err := fmt.Fprintf(w, "  %s%-8s%s %s~%d%s\n", colors.Label, "Tokens:", colors.Reset, colors.Value, summary.Tokens, colors.Reset); err != nil {
			return err
		}
	}
	return nil
}

// BypassesDirectoryLabel reports whether a directory label should inherit the
// bypass/error color for a directly targeted ignored subtree.
func BypassesDirectoryLabel(entry DocumentEntry, relDir string) bool {
	if !entry.Bypassed {
		return false
	}
	targetRoot := normalizeRelPath(entry.TargetRoot)
	if targetRoot != "" && targetRoot != "." && targetRoot != entry.Path {
		if relDir == targetRoot || strings.HasPrefix(relDir, targetRoot+"/") {
			return true
		}
	}
	if entry.BlockRule == "" || !strings.HasSuffix(entry.BlockRule, "/") {
		return false
	}
	ruleName := path.Base(strings.TrimSuffix(entry.BlockRule, "/"))
	return path.Base(relDir) == ruleName
}

func renderEmptyTarget(w io.Writer, target *DocumentTarget, opts RenderOptions, colors Palette) error {
	label := renderTargetLabel(target, opts)
	if label != "" {
		if _, err := fmt.Fprintf(w, "%s%s%s\n", colors.Dir, label, colors.Reset); err != nil {
			return err
		}
	}

	message := "no previewable text files"
	switch target.Kind {
	case TargetKindDir:
		switch target.State {
		case TargetStateEmpty:
			message = "empty directory"
		case TargetStateNoTextChildren:
			message = "no previewable text files"
		}
	case TargetKindFile:
		if target.State == TargetStateNonText {
			message = "not a previewable text file"
		}
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

func renderEntries(w io.Writer, entries []DocumentEntry, opts RenderOptions, colors Palette) error {
	entries = SortedEntries(entries)
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
		for i := 0; i < fileIndex; i++ {
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
			if BypassesDirectoryLabel(entry, accum) {
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
		if entry.Bypassed {
			nameColor = colors.Err
			nameReset = colors.Reset
		}

		fileLine := filePrefix + nameColor + parts[fileIndex] + nameReset
		if opts.ShowSizes && entry.Size != nil {
			sizeLabel := formatInlineSize(*entry.Size)
			if entry.Bypassed {
				fileLine += " " + colors.Err + "(" + sizeLabel + ")" + colors.Reset
			} else {
				fileLine += " " + styleSize(sizeLabel, *entry.Size, colors)
			}
		}
		if opts.ShowGitStatus && entry.GitStatus != "" {
			fileLine += " " + styleStatus(entry.GitStatus, colors)
		}
		if !opts.Bare && entry.ModeTag != "" {
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
		content = highlightFilePreview(file.Path, file.Content)
	}

	lines := splitPreviewLines(content)
	lines = overlayPreviewMatchHighlights(lines, rawLines, file.MatchPattern)
	truncated := file.Truncated
	if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
		lines = lines[:opts.MaxLines]
		truncated = true
	}

	width := len(strconv.Itoa(len(lines)))
	if width < 1 {
		width = 1
	}
	for i, line := range lines {
		if _, err := fmt.Fprintf(w, "%s%*d%s %s│%s %s\n", colors.Label, width, i+1, colors.Reset, colors.Tree, colors.Reset, line); err != nil {
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

func highlightFilePreview(relPath, content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}

	lexer := lexers.Match(relPath)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil || lexer == lexers.Fallback {
		return content
	}

	lexer = chroma.Coalesce(lexer)
	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}

	style := styles.Get(defaultChromaStyle)
	if style == nil {
		style = styles.Fallback
	}

	var buf bytes.Buffer
	if err := formatters.TTY256.Format(&buf, style, iterator); err != nil {
		return content
	}
	return buf.String()
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

	re, err := regexp.Compile(pattern)
	if err != nil {
		return lines
	}

	limit := len(lines)
	if len(rawLines) < limit {
		limit = len(rawLines)
	}
	for i := 0; i < limit; i++ {
		lines[i] = highlightMatchLineANSI(lines[i], rawLines[i], re)
	}
	return lines
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
		for _, segment := range strings.Split(dir, "/") {
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

func splitLogicalLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	lines := make([]string, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for start < len(data) {
		offset := bytes.IndexByte(data[start:], '\n')
		if offset < 0 {
			lines = append(lines, string(data[start:]))
			break
		}
		end := start + offset
		lines = append(lines, string(data[start:end]))
		start = end + 1
	}
	if len(lines) == 0 && utf8.Valid(data) {
		return []string{string(data)}
	}
	return lines
}
