package catclip

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tigreau/catclip/fileclip"
)

var fileclipCopy = fileclip.Copy

var (
	fileCloseTag            = []byte("</file>\n\n")
	fileCloseTagWithNewline = []byte("\n</file>\n\n")
)

type emitStats struct {
	PayloadBytes          int64
	GenerateDuration      time.Duration
	SinkFinalizeDuration  time.Duration
	ClipboardWaitDuration time.Duration
	SinkName              string
	BundlePath            string
	Warnings              []string
}

type emitConfig struct {
	OutputMode outputMode
	Raw        bool
	NoBundle   bool
}

// emitEnvironment is the temporary Phase 8 stand-in for invocationConfig's
// environment fields. Phase 9 should replace this with invocationConfig.
type emitEnvironment struct {
	Platform   string
	WorkingDir string
}

const bundleThreshold = 4096
const minimumSandboxPortalMajor = 1
const minimumSandboxPortalMinor = 21

func bundleTempDir() string {
	return filepath.Join(os.TempDir(), "catclip")
}

func bundleDirForEnv(env emitEnvironment) string {
	if dir := strings.TrimSpace(os.Getenv("CATCLIP_BUNDLE_DIR")); dir != "" {
		return dir
	}
	// Keep bundle files in a user-visible Documents folder instead of a temp
	// directory. Sandboxed browsers such as Firefox Snap can attach files from
	// normal home folders, but not from /tmp; hidden cache/state dirs have the
	// same problem. A dedicated catclip subdirectory keeps cleanup scoped.
	if dir := userDocumentsDir(env.Platform); dir != "" {
		return filepath.Join(dir, "catclip")
	}
	return bundleTempDir()
}

func userDocumentsDir(platform string) string {
	if platform == "linux" {
		return xdgUserDir("XDG_DOCUMENTS_DIR", "Documents")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Documents")
}

func xdgUserDir(key, fallback string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}

	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	data, err := os.ReadFile(filepath.Join(configHome, "user-dirs.dirs"))
	if err == nil {
		prefix := key + "="
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			value := strings.TrimPrefix(line, prefix)
			value = strings.Trim(value, `"`)
			value = strings.ReplaceAll(value, "$HOME", home)
			if filepath.IsAbs(value) {
				return filepath.Clean(value)
			}
		}
	}

	return filepath.Join(home, fallback)
}

func bundleProjectName(workingDir string) string {
	base := filepath.Base(workingDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "bundle"
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "bundle"
	}
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

func bundleTempPath(dir, projectName string, now time.Time) string {
	stamp := formatBundleTimestamp(now)
	return filepath.Join(dir, fmt.Sprintf("%s-%s.txt", projectName, stamp))
}

func clearPriorBundles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if isCatclipBundleName(entry.Name()) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func isCatclipBundleName(name string) bool {
	if !strings.HasSuffix(name, ".txt") {
		return false
	}
	stem := strings.TrimSuffix(name, ".txt")
	if len(stem) < len("a-000000") {
		return false
	}
	dash := strings.LastIndex(stem, "-")
	if dash <= 0 || dash != len(stem)-7 {
		return false
	}
	for _, r := range stem[dash+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type countingWriter struct {
	w io.Writer
	n int64
}

type emitPrefetchResult struct {
	index      int
	data       []byte
	prefetched bool
	err        error
}

type emitPrefetcher struct {
	done      chan struct{}
	results   chan emitPrefetchResult
	pending   map[int]emitPrefetchResult
	closeOnce sync.Once
	closed    chan struct{}
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.n += int64(n)
	return n, err
}

// emitFullOutput writes every prepared output unit either to stdout or through
// the platform clipboard command.
func emitFullOutput(cfg emitConfig, env emitEnvironment, units []preparedFileUnit, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return emitOutputPlan(cfg, env, buildOutputPlan(units), stdout, colors)
}

func emitOutputPlan(cfg emitConfig, env emitEnvironment, plan outputPlan, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return withPayloadWriter(cfg, env, stdout, colors, func(w io.Writer) error {
		return writeOutputPlanPayload(w, cfg, plan)
	})
}

func emitRawOutputPlan(cfg emitConfig, env emitEnvironment, plan outputPlan, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return withPayloadWriter(cfg, env, stdout, colors, func(w io.Writer) error {
		return writeRawOutputPlanPayload(w, plan)
	})
}

func writeOutputPlanPayload(w io.Writer, cfg emitConfig, plan outputPlan) error {
	return writeOutputPlanPayloadWithPrefetch(w, cfg, plan, true)
}

func writeOutputPlanPayloadWithoutPrefetch(w io.Writer, cfg emitConfig, plan outputPlan) error {
	return writeOutputPlanPayloadWithPrefetch(w, cfg, plan, false)
}

func writeOutputPlanPayloadWithPrefetch(w io.Writer, cfg emitConfig, plan outputPlan, prefetch bool) error {
	if cfg.Raw {
		return writeRawOutputPlanPayload(w, plan)
	}
	if plan.HasPaths() {
		return writeSectionedOutputPlanPayload(w, plan, prefetch)
	}
	return writeFileOutputPlanPayload(w, plan, prefetch)
}

func writeRawOutputPlanPayload(w io.Writer, plan outputPlan) error {
	for _, item := range plan.items {
		if item.kind != outputSectionKindFiles || (item.mode != entryModeFull && item.mode != entryModeLines) {
			return fmt.Errorf("raw output requires full-file or lines items")
		}
		entry := item.unit.Entry
		if entry.Lines {
			if err := emitLinesSliceRaw(w, entry.AbsPath, entry.LinesStart, entry.LinesEnd); err != nil {
				return err
			}
			continue
		}
		if err := emitFileBodyFromDisk(w, entry.AbsPath); err != nil {
			return err
		}
	}
	return nil
}

// emitLinesSliceRaw streams lines [linesStart, linesEnd] of absPath with no
// formatting — no XML wrapper, no `cat -n`-style line numbers. The slice
// is the bare bytes of the matching lines, each terminated by \n.
func emitLinesSliceRaw(w io.Writer, absPath string, linesStart, linesEnd int) error {
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if linesStart > 0 && lineNum < linesStart {
			continue
		}
		if linesEnd > 0 && lineNum > linesEnd {
			break
		}
		if _, err := w.Write(scanner.Bytes()); err != nil {
			return fmt.Errorf("failed while writing %s: %w", absPath, err)
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("failed while writing %s: %w", absPath, err)
		}
	}
	return scanner.Err()
}

func emitNumberedFileBodyFromDisk(w io.Writer, absPath string) error {
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()

	nw := &numberedWriter{w: w, line: 1, atLineStart: true}
	buf := make([]byte, readBufferSize())
	if _, err := io.CopyBuffer(nw, f, buf); err != nil {
		return fmt.Errorf("failed while streaming %s: %w", absPath, err)
	}
	return nil
}

// emitLinesFile writes a wrapped file with line numbering. For sliced mode
// (LinesStart > 0), it adds a lines="START-END" attribute and only emits
// the requested range. For bare --lines, it numbers the entire file.
//
// In sliced mode, if the file has no lines in the requested range (start
// line exceeds file length), zero bytes are emitted — no open tag, no
// close tag. This matches the convention used by sed, awk, Python
// slicing, and every line-range tool surveyed: short files contribute
// nothing rather than empty wrappers. The picker preview path inherits
// this behavior automatically because it routes through the same emit
// pipeline.
func emitLinesFile(w io.Writer, entry fileEntry) error {
	f, err := os.Open(entry.AbsPath)
	if err != nil {
		return err
	}
	defer f.Close()

	sliced := entry.LinesStart > 0
	linesAttr := ""
	if sliced {
		if entry.LinesEnd > 0 {
			linesAttr = fmt.Sprintf("%d-%d", entry.LinesStart, entry.LinesEnd)
		} else {
			linesAttr = fmt.Sprintf("%d-", entry.LinesStart)
		}
	}

	openTag := buildFileOpenTagWithLines(entry.RelPath, "", linesAttr)
	startLine := 1
	if sliced {
		startLine = entry.LinesStart
	}

	// For bare --lines (full file), write the open tag eagerly so empty
	// files still get a wrapper. For sliced mode, defer it until the
	// first matching line so short files emit zero bytes.
	openWritten := false
	if !sliced {
		if _, err := w.Write(openTag); err != nil {
			return err
		}
		openWritten = true
	}

	nw := &numberedWriter{w: w, line: startLine, atLineStart: true}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	lineNum := 0
	var lastByte byte
	wroteAny := false

	for scanner.Scan() {
		lineNum++
		if sliced && lineNum < entry.LinesStart {
			continue
		}
		if entry.LinesEnd > 0 && lineNum > entry.LinesEnd {
			break
		}
		if !openWritten {
			if _, err := w.Write(openTag); err != nil {
				return err
			}
			openWritten = true
		}
		line := scanner.Bytes()
		if _, err := nw.Write(line); err != nil {
			return fmt.Errorf("failed while writing %s: %w", entry.RelPath, err)
		}
		if _, err := nw.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("failed while writing %s: %w", entry.RelPath, err)
		}
		wroteAny = true
		lastByte = '\n'
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed while streaming %s: %w", entry.AbsPath, err)
	}

	if !openWritten {
		// Sliced mode, no lines in range — emit nothing.
		return nil
	}
	if wroteAny && lastByte != '\n' {
		_, err := w.Write(fileCloseTagWithNewline)
		return err
	}
	_, err = w.Write(fileCloseTag)
	return err
}

// emitLinesFileBody writes file content with line numbering to a raw writer
// (no XML wrapper). For sliced mode, only emits the requested range.
func emitLinesFileBody(w io.Writer, absPath string, linesStart, linesEnd int) error {
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()

	startLine := 1
	if linesStart > 0 {
		startLine = linesStart
	}
	nw := &numberedWriter{w: w, line: startLine, atLineStart: true}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		if linesStart > 0 && lineNum < linesStart {
			continue
		}
		if linesEnd > 0 && lineNum > linesEnd {
			break
		}
		line := scanner.Bytes()
		if _, err := nw.Write(line); err != nil {
			return fmt.Errorf("failed while writing %s: %w", absPath, err)
		}
		if _, err := nw.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("failed while writing %s: %w", absPath, err)
		}
	}
	return scanner.Err()
}

type numberedWriter struct {
	w           io.Writer
	line        int
	atLineStart bool
}

func (nw *numberedWriter) Write(p []byte) (int, error) {
	consumed := 0
	for len(p) > 0 {
		if nw.atLineStart {
			prefix := formatLineNumber(nw.line)
			if _, err := nw.w.Write(prefix); err != nil {
				return consumed, err
			}
			nw.atLineStart = false
		}
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			n, err := nw.w.Write(p)
			consumed += n
			return consumed, err
		}
		n, err := nw.w.Write(p[:idx+1])
		consumed += n
		if err != nil {
			return consumed, err
		}
		nw.line++
		nw.atLineStart = true
		p = p[idx+1:]
	}
	return consumed, nil
}

func formatLineNumber(n int) []byte {
	if n <= 999999 {
		return []byte(fmt.Sprintf("%6d\t", n))
	}
	return []byte(fmt.Sprintf("%d\t", n))
}

func emitSectionedOutputPlan(cfg emitConfig, env emitEnvironment, plan outputPlan, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return withPayloadWriter(cfg, env, stdout, colors, func(w io.Writer) error {
		return writeSectionedOutputPlanPayload(w, plan, true)
	})
}

func writeSectionedOutputPlanPayload(w io.Writer, plan outputPlan, prefetch bool) error {
	for i, section := range plan.sections {
		if i > 0 {
			separator := outputSectionSeparator(plan.sections[i-1].kind, section.kind)
			if separator != "" {
				if _, err := io.WriteString(w, separator); err != nil {
					return err
				}
			}
		}
		switch section.kind {
		case outputSectionKindPaths:
			for _, item := range section.items {
				if _, err := io.WriteString(w, item.relPath+"\n"); err != nil {
					return err
				}
			}
		case outputSectionKindFiles:
			subplan := outputPlan{items: section.items}
			if err := writeFileOutputPlanPayload(w, subplan, prefetch); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFileOutputPlanPayload(w io.Writer, plan outputPlan, prefetch bool) error {
	var prefetcher *emitPrefetcher
	if prefetch {
		prefetcher = startEmitPrefetch(plan)
	}
	if prefetcher != nil {
		defer prefetcher.Close()
	}
	for i, item := range plan.items {
		if err := emitEntry(w, item.unit, i, prefetcher); err != nil {
			return err
		}
	}
	return nil
}

func outputSectionSeparator(prev, next outputSectionKind) string {
	if prev == outputSectionKindPaths && next == outputSectionKindFiles {
		return "\n"
	}
	if prev == outputSectionKindFiles && next == outputSectionKindPaths {
		return ""
	}
	if prev != next {
		return "\n"
	}
	return ""
}

func emitEntry(w io.Writer, unit preparedFileUnit, index int, prefetcher *emitPrefetcher) error {
	if len(unit.Payload) > 0 {
		_, err := w.Write(unit.Payload)
		return err
	}
	if len(unit.SnippetRanges) > 0 {
		return emitSnippetRangesFile(w, unit.Entry, unit.SnippetRanges)
	}
	return emitFile(w, unit, index, prefetcher)
}

func emitSnippetRangesFile(w io.Writer, entry fileEntry, ranges []snippetRange) error {
	f, err := os.Open(entry.AbsPath)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	rangeIndex := 0
	lineNum := 0
	inRange := false
	for scanner.Scan() {
		lineNum++
		for rangeIndex < len(ranges) && lineNum > ranges[rangeIndex].End {
			if inRange {
				if _, err := w.Write(fileCloseTag); err != nil {
					return err
				}
				inRange = false
			}
			rangeIndex++
		}
		if rangeIndex >= len(ranges) {
			break
		}
		current := ranges[rangeIndex]
		if lineNum < current.Start {
			continue
		}
		if !inRange {
			if _, err := w.Write(buildFileOpenTagWithLines(entry.RelPath, "", fmt.Sprintf("%d-%d", current.Start, current.End))); err != nil {
				return err
			}
			inRange = true
		}
		if _, err := w.Write(scanner.Bytes()); err != nil {
			return fmt.Errorf("failed while writing %s: %w", entry.AbsPath, err)
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("failed while writing %s: %w", entry.AbsPath, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if inRange {
		if _, err := w.Write(fileCloseTag); err != nil {
			return err
		}
	}
	return nil
}

func emitFile(w io.Writer, unit preparedFileUnit, index int, prefetcher *emitPrefetcher) error {
	entry := unit.Entry
	if entry.Lines {
		return emitLinesFile(w, entry)
	}
	if prefetcher != nil {
		result, err := prefetcher.Wait(index)
		if err != nil {
			return err
		}
		if result.err != nil {
			return result.err
		}
		if result.prefetched {
			return emitWrappedFile(w, entry.RelPath, "", result.data)
		}
	}
	return emitFileFromDisk(w, entry.RelPath, "", entry.AbsPath)
}

func emitFileFromDisk(w io.Writer, relPath, typeAttr, absPath string) error {
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return emitWrappedReader(w, relPath, typeAttr, f)
}

func emitFileBodyFromDisk(w io.Writer, absPath string) error {
	f, err := os.Open(absPath)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, readBufferSize())
	if _, err := io.CopyBuffer(w, f, buf); err != nil {
		return fmt.Errorf("failed while streaming %s: %w", absPath, err)
	}
	return nil
}

func emitWrappedFile(w io.Writer, relPath, typeAttr string, data []byte) error {
	return emitWrappedReader(w, relPath, typeAttr, bytes.NewReader(data))
}

func emitWrappedReader(w io.Writer, relPath, typeAttr string, r io.Reader) error {
	if _, err := w.Write(buildFileOpenTag(relPath, typeAttr)); err != nil {
		return err
	}
	readBufSize := readBufferSize()
	var (
		buf      = make([]byte, readBufSize)
		lastByte byte
		wroteAny bool
	)
	for {
		n, err := r.Read(buf[:])
		if n > 0 {
			wroteAny = true
			lastByte = buf[n-1]
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("failed while writing %s: %w", relPath, writeErr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed while streaming %s: %w", relPath, err)
		}
	}
	if wroteAny && lastByte != '\n' {
		_, err := w.Write(fileCloseTagWithNewline)
		return err
	}
	_, err := w.Write(fileCloseTag)
	return err
}

func startEmitPrefetch(plan outputPlan) *emitPrefetcher {
	workers := emitReadWorkerCount()
	capBytes := emitPrefetchFileCap()
	if workers <= 1 || capBytes <= 0 {
		return nil
	}

	type job struct {
		index int
		unit  preparedFileUnit
	}

	done := make(chan struct{})
	jobs := make(chan job, workers)
	results := make(chan emitPrefetchResult, workers)
	closed := make(chan struct{})

	var workerWG sync.WaitGroup
	workerWG.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer workerWG.Done()
			for current := range jobs {
				data, prefetched, err := readPrefetchCandidate(current.unit.Entry, capBytes)
				result := emitPrefetchResult{
					index:      current.index,
					data:       data,
					prefetched: prefetched,
					err:        err,
				}
				select {
				case results <- result:
				case <-done:
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i, item := range plan.items {
			unit := item.unit
			if !unitUsesFullOutput(unit) {
				continue
			}
			select {
			case jobs <- job{index: i, unit: unit}:
			case <-done:
				return
			}
		}
	}()

	go func() {
		workerWG.Wait()
		close(results)
		close(closed)
	}()

	return &emitPrefetcher{
		done:    done,
		results: results,
		pending: make(map[int]emitPrefetchResult, workers),
		closed:  closed,
	}
}

func (p *emitPrefetcher) Wait(index int) (emitPrefetchResult, error) {
	if p == nil {
		return emitPrefetchResult{}, nil
	}
	if result, ok := p.pending[index]; ok {
		delete(p.pending, index)
		return result, nil
	}
	for {
		result, ok := <-p.results
		if !ok {
			return emitPrefetchResult{}, fmt.Errorf("prefetch pipeline closed before result %d", index)
		}
		if result.index == index {
			return result, nil
		}
		p.pending[result.index] = result
	}
}

func (p *emitPrefetcher) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		close(p.done)
	})
	<-p.closed
}

func readPrefetchCandidate(entry fileEntry, capBytes int64) ([]byte, bool, error) {
	size := entry.SizeBytes
	if !entry.SizeKnown {
		info, err := os.Stat(entry.AbsPath)
		if err != nil {
			return nil, false, err
		}
		if !info.Mode().IsRegular() {
			return nil, false, nil
		}
		size = info.Size()
	}
	if size < 0 || size > capBytes {
		return nil, false, nil
	}

	data, err := os.ReadFile(entry.AbsPath)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func entryUsesFullOutput(entry fileEntry) bool {
	return entry.Mode == "" || entry.Mode == entryModeFull
}

func unitUsesFullOutput(unit preparedFileUnit) bool {
	return len(unit.Payload) == 0 && entryUsesFullOutput(unit.Entry)
}

func buildFileOpenTag(relPath, typeAttr string) []byte {
	return buildFileOpenTagWithLines(relPath, typeAttr, "")
}

func buildFileOpenTagWithLines(relPath, typeAttr, linesAttr string) []byte {
	size := len(relPath) + 16
	if typeAttr != "" {
		size += len(typeAttr) + 9
	}
	if linesAttr != "" {
		size += len(linesAttr) + 9
	}
	tag := make([]byte, 0, size)
	tag = append(tag, "<file path=\""...)
	tag = append(tag, relPath...)
	tag = append(tag, '"')
	if typeAttr != "" {
		tag = append(tag, " type=\""...)
		tag = append(tag, typeAttr...)
		tag = append(tag, '"')
	}
	if linesAttr != "" {
		tag = append(tag, " lines=\""...)
		tag = append(tag, linesAttr...)
		tag = append(tag, '"')
	}
	tag = append(tag, ">\n"...)
	return tag
}

type snippetRange struct {
	Start int
	End   int
}

// emitDiffEntry emits git patches for tracked files and falls back to full
// content for untracked files, matching the shell tool's diff UX.
func emitDiffEntry(w io.Writer, gitCtx gitContext, entry fileEntry) error {
	payload, _, keep, err := buildPreparedDiffPayload(gitCtx, entry)
	if err != nil {
		return err
	}
	if !keep {
		return nil
	}
	_, err = w.Write(payload)
	return err
}

func diffEntryOutput(gitCtx gitContext, entry fileEntry) (string, string, bool, error) {
	if !gitCtx.Enabled {
		return "", "", false, nil
	}

	repoPath := gitCtx.toRepoPath(entry.RelPath)
	trackedOutput, err := runGitCapture(gitCtx.Root, "ls-files", "--", repoPath)
	if err != nil {
		return "", "", false, err
	}
	if strings.TrimSpace(trackedOutput) == "" {
		return "", "", false, nil
	}

	var diffOutput string
	var diffType string
	switch {
	case entry.DiffWantStaged && !entry.DiffWantUnstaged:
		diffOutput, err = runGitCapture(gitCtx.Root, "diff", "--cached", "--", repoPath)
		diffType = "staged-diff"
	case entry.DiffWantUnstaged && !entry.DiffWantStaged:
		diffOutput, err = runGitCapture(gitCtx.Root, "diff", "--", repoPath)
		diffType = "unstaged-diff"
	default:
		diffOutput, err = diffAgainstHeadOrIndex(gitCtx, repoPath)
		diffType = "diff"
	}
	if err != nil {
		return "", "", true, err
	}
	return diffOutput, diffType, true, nil
}

func withPayloadWriter(cfg emitConfig, env emitEnvironment, stdout io.Writer, colors colorPalette, fn func(io.Writer) error) (emitStats, error) {
	bufferSize := outputBufferSize()

	if cfg.OutputMode == outputModeStdout {
		counted := &countingWriter{w: stdout}
		buffered := bufio.NewWriterSize(counted, bufferSize)
		generateStarted := time.Now()
		if err := fn(buffered); err != nil {
			return emitStats{}, err
		}
		generateDuration := time.Since(generateStarted)
		finalizeStarted := time.Now()
		if err := buffered.Flush(); err != nil {
			return emitStats{}, err
		}
		return emitStats{
			PayloadBytes:         counted.n,
			GenerateDuration:     generateDuration,
			SinkFinalizeDuration: time.Since(finalizeStarted),
			SinkName:             "stdout",
		}, nil
	}

	// Clipboard mode has two paths. With --no-bundle the user has opted out of
	// bundle dispatch entirely, so we preserve the old behavior: stream the
	// generator straight into the clipboard subprocess. Without --no-bundle we
	// have to materialize the payload first because the bundle/text decision
	// depends on its size.
	if cfg.NoBundle {
		return streamToTextClipboard(env, fn, colors, bufferSize)
	}

	var payloadBuf bytes.Buffer
	counted := &countingWriter{w: &payloadBuf}
	buffered := bufio.NewWriterSize(counted, bufferSize)
	generateStarted := time.Now()
	if err := fn(buffered); err != nil {
		return emitStats{}, err
	}
	if err := buffered.Flush(); err != nil {
		return emitStats{}, err
	}
	generateDuration := time.Since(generateStarted)
	payload := payloadBuf.Bytes()

	if len(payload) >= bundleThreshold {
		return emitBundle(env, payload, generateDuration, colors)
	}

	return emitBufferedToTextClipboard(env, payload, generateDuration, colors)
}

func streamToTextClipboard(env emitEnvironment, fn func(io.Writer) error, colors colorPalette, bufferSize int) (emitStats, error) {
	cmd, err := clipboardCommand(env.Platform, colors)
	if err != nil {
		return emitStats{}, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return emitStats{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return emitStats{}, err
	}

	counted := &countingWriter{w: stdin}
	buffered := bufio.NewWriterSize(counted, bufferSize)
	generateStarted := time.Now()
	writeErr := fn(buffered)
	generateDuration := time.Since(generateStarted)
	finalizeStarted := time.Now()
	flushErr := buffered.Flush()
	closeErr := stdin.Close()
	finalizeDuration := time.Since(finalizeStarted)
	waitStarted := time.Now()
	waitErr := waitClipboardCommand(cmd, env.Platform)
	waitDuration := time.Since(waitStarted)

	if writeErr != nil {
		return emitStats{}, writeErr
	}
	if flushErr != nil {
		return emitStats{}, flushErr
	}
	if closeErr != nil {
		return emitStats{}, closeErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return emitStats{}, fmt.Errorf("Error: clipboard command failed: %s", msg)
		}
		return emitStats{}, fmt.Errorf("Error: clipboard command failed: %w", waitErr)
	}
	return emitStats{
		PayloadBytes:          counted.n,
		GenerateDuration:      generateDuration,
		SinkFinalizeDuration:  finalizeDuration,
		ClipboardWaitDuration: waitDuration,
		SinkName:              filepath.Base(cmd.Path),
	}, nil
}

func emitBufferedToTextClipboard(env emitEnvironment, payload []byte, generateDuration time.Duration, colors colorPalette) (emitStats, error) {
	cmd, err := clipboardCommand(env.Platform, colors)
	if err != nil {
		return emitStats{}, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return emitStats{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return emitStats{}, err
	}

	finalizeStarted := time.Now()
	_, writeErr := stdin.Write(payload)
	closeErr := stdin.Close()
	finalizeDuration := time.Since(finalizeStarted)
	waitStarted := time.Now()
	waitErr := waitClipboardCommand(cmd, env.Platform)
	waitDuration := time.Since(waitStarted)

	if writeErr != nil {
		return emitStats{}, writeErr
	}
	if closeErr != nil {
		return emitStats{}, closeErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return emitStats{}, fmt.Errorf("Error: clipboard command failed: %s", msg)
		}
		return emitStats{}, fmt.Errorf("Error: clipboard command failed: %w", waitErr)
	}
	return emitStats{
		PayloadBytes:          int64(len(payload)),
		GenerateDuration:      generateDuration,
		SinkFinalizeDuration:  finalizeDuration,
		ClipboardWaitDuration: waitDuration,
		SinkName:              filepath.Base(cmd.Path),
	}, nil
}

func emitBundle(env emitEnvironment, payload []byte, generateDuration time.Duration, colors colorPalette) (emitStats, error) {
	dir := bundleDirForEnv(env)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return emitStats{}, fmt.Errorf("Error: bundle directory: %w", err)
	}
	clearPriorBundles(dir)

	finalizeStarted := time.Now()
	tmpPath := bundleTempPath(dir, bundleProjectName(env.WorkingDir), time.Now())
	if err := os.WriteFile(tmpPath, payload, 0600); err != nil {
		return emitStats{}, fmt.Errorf("Error: bundle write: %w", err)
	}
	if err := fileclipCopy(tmpPath); err != nil {
		if errors.Is(err, fileclip.ErrX11Unsupported) || errors.Is(err, fileclip.ErrLegacyGNOMEUnsupported) {
			return emitStats{}, fmt.Errorf(
				"Error: %s. Nothing was placed on your clipboard.\n\n"+
					"Your catclip bundle was saved to:\n  %s\n\n"+
					"Drag it into the target application, or copy it from your file manager.\n\n"+
					"For text clipboard output, rerun with --no-bundle.\n"+
					"%s",
				unsupportedFileClipboardReason(err),
				tmpPath,
				unsupportedFileClipboardRemedy(err),
			)
		}
		_ = os.Remove(tmpPath)
		// fileclip.ErrToolNotFound is the "no clipboard binary on PATH"
		// sentinel. Render the same multi-distro install hint the text
		// clipboard path uses so the two sinks teach the user identically.
		// Other failures (ErrToolFailed or anything else) keep the
		// generic "clipboard command failed" framing — those are runtime
		// failures from a present tool, not a missing-tool situation.
		if errors.Is(err, fileclip.ErrToolNotFound) {
			return emitStats{}, fmt.Errorf("Error: No clipboard tool found.\n%s", clipboardInstallHint(env.Platform, colors))
		}
		return emitStats{}, fmt.Errorf("Error: clipboard command failed: %w", err)
	}
	finalizeDuration := time.Since(finalizeStarted)

	return emitStats{
		PayloadBytes:         int64(len(payload)),
		GenerateDuration:     generateDuration,
		SinkFinalizeDuration: finalizeDuration,
		SinkName:             "bundle",
		BundlePath:           tmpPath,
		Warnings:             bundleWarnings(env),
	}, nil
}

func unsupportedFileClipboardReason(err error) string {
	if errors.Is(err, fileclip.ErrX11Unsupported) {
		return "X11 file-reference clipboard is not supported"
	}
	if errors.Is(err, fileclip.ErrLegacyGNOMEUnsupported) {
		return fmt.Sprintf(
			"GNOME below %d file-reference clipboard is not supported",
			fileclip.MinimumGNOMEFileClipboardMajor,
		)
	}
	return "file-reference clipboard is not supported"
}

func unsupportedFileClipboardRemedy(err error) string {
	if errors.Is(err, fileclip.ErrX11Unsupported) {
		return "For one-step paste, log into a Wayland session."
	}
	if errors.Is(err, fileclip.ErrLegacyGNOMEUnsupported) {
		return fmt.Sprintf(
			"For one-step paste, upgrade to GNOME %d or newer.",
			fileclip.MinimumGNOMEFileClipboardMajor,
		)
	}
	return ""
}

func bundleWarnings(env emitEnvironment) []string {
	if env.Platform != "linux" || !isWaylandSession() {
		return nil
	}
	if warning, ok := sandboxPortalWarning(); ok {
		return []string{
			warning,
		}
	}
	return nil
}

func sandboxPortalWarning() (string, bool) {
	major, minor, ok := xdgDesktopPortalVersion()
	if !ok {
		return "xdg-desktop-portal was not found, so sandboxed app paste support could not be verified. Sandboxed apps such as Firefox Snap may not attach this bundle from the clipboard; drag and drop the file if paste fails.", true
	}
	if !portalVersionBelowSandboxBaseline(major, minor) {
		return "", false
	}
	return fmt.Sprintf("xdg-desktop-portal %d.%d is older than the recommended %d.%d baseline. Sandboxed apps such as Firefox Snap may not attach this bundle from the clipboard; drag and drop the file if paste fails.", major, minor, minimumSandboxPortalMajor, minimumSandboxPortalMinor), true
}

func portalVersionBelowSandboxBaseline(major, minor int) bool {
	if major != minimumSandboxPortalMajor {
		return major < minimumSandboxPortalMajor
	}
	return minor < minimumSandboxPortalMinor
}

func xdgDesktopPortalVersion() (int, int, bool) {
	raw := strings.TrimSpace(os.Getenv("CATCLIP_XDG_DESKTOP_PORTAL_VERSION"))
	if raw == "" {
		out, err := runXDGDesktopPortalVersionCommand()
		if err != nil {
			return 0, 0, false
		}
		raw = string(out)
	}
	return parseMajorMinorVersion(raw)
}

func runXDGDesktopPortalVersionCommand() ([]byte, error) {
	if bin := strings.TrimSpace(os.Getenv("CATCLIP_XDG_DESKTOP_PORTAL_BIN")); bin != "" {
		return exec.Command(bin, "--version").Output()
	}
	var lastErr error
	for _, bin := range xdgDesktopPortalVersionCandidatePaths() {
		out, err := exec.Command(bin, "--version").Output()
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func xdgDesktopPortalVersionCandidatePaths() []string {
	return []string{
		"xdg-desktop-portal",
		"/usr/libexec/xdg-desktop-portal",
		"/usr/lib/xdg-desktop-portal",
		"/usr/lib/xdg-desktop-portal/xdg-desktop-portal",
	}
}

func parseMajorMinorVersion(raw string) (int, int, bool) {
	for _, field := range strings.Fields(raw) {
		field = strings.TrimLeft(field, "vV")
		if field == "" || field[0] < '0' || field[0] > '9' {
			continue
		}
		parts := strings.SplitN(field, ".", 3)
		if len(parts) < 2 {
			continue
		}
		major, majorErr := strconv.Atoi(parts[0])
		minorText := parts[1]
		if idx := strings.IndexAny(minorText, "-+"); idx >= 0 {
			minorText = minorText[:idx]
		}
		minor, minorErr := strconv.Atoi(minorText)
		if majorErr == nil && minorErr == nil {
			return major, minor, true
		}
	}
	return 0, 0, false
}

func waitClipboardCommand(cmd *exec.Cmd, platform string) error {
	if !clipboardCommandMayStayResident(cmd, platform) {
		return cmd.Wait()
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- cmd.Wait()
	}()

	timeout := clipboardWaitTimeout()
	if timeout <= 0 {
		return <-errCh
	}

	select {
	case err := <-errCh:
		return err
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
		return nil
	}
}

func clipboardCommandMayStayResident(cmd *exec.Cmd, platform string) bool {
	if platform == "macos" || platform == "wsl" {
		return false
	}
	switch filepath.Base(cmd.Path) {
	// xsel was previously in this list. Removed when clipboardCommand
	// stopped falling back to xsel — see the comment there for the
	// file-reference MIME target rationale that ties the text and bundle
	// paths to xclip / wl-clipboard.
	case "wl-copy", "xclip":
		return true
	default:
		return false
	}
}

func clipboardWaitTimeout() time.Duration {
	const defaultWait = 250 * time.Millisecond

	raw := strings.TrimSpace(os.Getenv("CATCLIP_CLIPBOARD_WAIT_MS"))
	if raw == "" {
		return defaultWait
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms < 0 {
		return defaultWait
	}
	return time.Duration(ms) * time.Millisecond
}

func outputBufferSize() int {
	const defaultSize = 64 * 1024

	raw := strings.TrimSpace(os.Getenv("CATCLIP_OUTPUT_BUFFER_KIB"))
	if raw == "" {
		return defaultSize
	}
	kib, err := strconv.Atoi(raw)
	if err != nil || kib <= 0 {
		return defaultSize
	}
	return kib * 1024
}

func readBufferSize() int {
	const defaultSize = 32 * 1024

	raw := strings.TrimSpace(os.Getenv("CATCLIP_READ_BUFFER_KIB"))
	if raw == "" {
		return defaultSize
	}
	kib, err := strconv.Atoi(raw)
	if err != nil || kib <= 0 {
		return defaultSize
	}
	return kib * 1024
}

func emitReadWorkerCount() int {
	const defaultWorkers = 2

	raw := strings.TrimSpace(os.Getenv("CATCLIP_READ_WORKERS"))
	if raw == "" {
		return defaultWorkers
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count <= 0 {
		return defaultWorkers
	}
	return count
}

func emitPrefetchFileCap() int64 {
	const defaultSize = 4 * 1024 * 1024

	raw := strings.TrimSpace(os.Getenv("CATCLIP_PREFETCH_FILE_KIB"))
	if raw == "" {
		return defaultSize
	}
	kib, err := strconv.Atoi(raw)
	if err != nil || kib <= 0 {
		return defaultSize
	}
	return int64(kib) * 1024
}

func clipboardCommand(platform string, colors colorPalette) (*exec.Cmd, error) {
	switch platform {
	case "macos":
		if _, err := exec.LookPath("pbcopy"); err != nil {
			return nil, fmt.Errorf("Error: No clipboard tool found.\n%s", clipboardInstallHint(platform, colors))
		}
		return exec.Command("pbcopy"), nil
	case "windows", "wsl":
		if path, err := exec.LookPath("clip.exe"); err == nil {
			return exec.Command(path), nil
		}
		if path, err := exec.LookPath("clip"); err == nil {
			return exec.Command(path), nil
		}
		return nil, fmt.Errorf("Error: No clipboard tool found.\n%s", clipboardInstallHint(platform, colors))
	default:
		if isWaylandSession() {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return exec.Command("wl-copy"), nil
			}
		}
		if _, err := exec.LookPath("xclip"); err == nil {
			return exec.Command("xclip", "-selection", "clipboard"), nil
		}
		// xsel is intentionally not a fallback. The bundle path (file-ref
		// clipboard) needs explicit file-reference MIME targets such as
		// text/uri-list or x-special/gnome-copied-files, which xclip can
		// serve reliably. Standardizing on xclip keeps the text and bundle
		// clipboard paths in lockstep — a user with xsel only would succeed
		// at text clipboard but fail at bundles, which is the kind of
		// asymmetry the v0.5.2 install-hint cleanup is designed to prevent.
		return nil, fmt.Errorf("Error: No clipboard tool found.\n%s", clipboardInstallHint(platform, colors))
	}
}

func isWaylandSession() bool {
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || os.Getenv("WAYLAND_DISPLAY") != ""
}

func clipboardInstallHint(platform string, colors colorPalette) string {
	switch platform {
	case "macos":
		return fmt.Sprintf("  %sEnsure pbcopy is in PATH (ships with macOS).%s", colors.Dim, colors.Reset)
	case "windows":
		return fmt.Sprintf("  %sEnsure clip.exe is available (ships with Windows).%s", colors.Dim, colors.Reset)
	case "wsl":
		return fmt.Sprintf("  %sEnsure clip.exe is reachable through WSL interop from the Windows host.%s", colors.Dim, colors.Reset)
	default:
		// Single hint message used by both the text and bundle clipboard
		// paths. The bundle path requires explicit file-reference MIME
		// targets such as text/uri-list or x-special/gnome-copied-files,
		// which xclip and wl-clipboard can serve but xsel cannot reliably
		// serve. Standardizing the user on xclip / wl-clipboard avoids the
		// asymmetry where text clipboard succeeds and bundle clipboard fails.
		if isWaylandSession() {
			return fmt.Sprintf("  Wayland detected. Install wl-clipboard:\n    sudo apt install wl-clipboard    %s# Debian/Ubuntu%s\n    sudo pacman -S wl-clipboard      %s# Arch%s\n    sudo dnf install wl-clipboard    %s# Fedora%s",
				colors.Dim, colors.Reset, colors.Dim, colors.Reset, colors.Dim, colors.Reset)
		}
		return fmt.Sprintf("  X11 detected. Install xclip:\n    sudo apt install xclip           %s# Debian/Ubuntu%s\n    sudo pacman -S xclip             %s# Arch%s\n    sudo dnf install xclip           %s# Fedora%s",
			colors.Dim, colors.Reset, colors.Dim, colors.Reset, colors.Dim, colors.Reset)
	}
}
