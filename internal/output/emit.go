package output

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
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
)

var FileclipCopy = fileclip.Copy

var (
	fileCloseTag            = []byte("</file>\n\n")
	fileCloseTagWithNewline = []byte("\n</file>\n\n")
)

type EmitStats struct {
	PayloadBytes          int64
	GenerateDuration      time.Duration
	SinkFinalizeDuration  time.Duration
	ClipboardWaitDuration time.Duration
	SinkName              string
	BundlePath            string
	Warnings              []string
}

type EmitConfig struct {
	OutputMode command.OutputMode
	Raw        bool
	NoBundle   bool
}

// EmitEnvironment is the temporary Phase 8 stand-in for command.Invocation's
// environment fields. Phase 9 should replace this with command.Invocation.
type EmitEnvironment struct {
	Platform   string
	WorkingDir string
}

const BundleThreshold = 4096
const minimumSandboxPortalMajor = 1
const minimumSandboxPortalMinor = 21

func bundleTempDir() string {
	return filepath.Join(os.TempDir(), "catclip")
}

func BundleDirForEnv(env EmitEnvironment) string {
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

func BundleProjectName(workingDir string) string {
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

func BundleTempPath(dir, projectName string, now time.Time) string {
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

// EmitFullOutput writes every prepared output unit either to stdout or through
// the platform clipboard command.
func EmitFullOutput(cfg EmitConfig, env EmitEnvironment, units []PreparedFileUnit, stdout io.Writer, colors platform.Palette) (EmitStats, error) {
	return EmitOutputPlan(cfg, env, BuildPlan(units), stdout, colors)
}

func EmitOutputPlan(cfg EmitConfig, env EmitEnvironment, plan Plan, stdout io.Writer, colors platform.Palette) (EmitStats, error) {
	return WithPayloadWriter(cfg, env, stdout, colors, func(w io.Writer) error {
		return WriteOutputPlanPayload(w, cfg, plan)
	})
}

func WriteOutputPlanPayload(w io.Writer, cfg EmitConfig, plan Plan) error {
	return writeOutputPlanPayloadWithPrefetch(w, cfg, plan, true)
}

func WriteOutputPlanPayloadWithoutPrefetch(w io.Writer, cfg EmitConfig, plan Plan) error {
	return writeOutputPlanPayloadWithPrefetch(w, cfg, plan, false)
}

func writeOutputPlanPayloadWithPrefetch(w io.Writer, cfg EmitConfig, plan Plan, prefetch bool) error {
	if cfg.Raw {
		return WriteRawOutputPlanPayload(w, plan)
	}
	finish := platform.InternalBenchSpan("output.write_payload.iterate_entries",
		"items", platform.InternalBenchInt(plan.Len()),
		"prefetch", platform.InternalBenchBool(prefetch),
	)
	bc := &emitByteCounter{w: w}
	var err error
	if plan.HasPaths() {
		err = writeSectionedOutputPlanPayload(bc, plan, prefetch)
	} else {
		err = writeFileOutputPlanPayload(bc, plan, prefetch)
	}
	finish(
		"bytes", strconv.FormatInt(bc.n, 10),
		"err", platform.InternalBenchError(err),
	)
	return err
}

type emitByteCounter struct {
	w io.Writer
	n int64
}

func (bc *emitByteCounter) Write(p []byte) (int, error) {
	n, err := bc.w.Write(p)
	bc.n += int64(n)
	return n, err
}

func WriteRawOutputPlanPayload(w io.Writer, plan Plan) error {
	for _, item := range plan.items {
		if item.kind != SectionKindFiles || (item.mode != command.EntryModeFull && item.mode != command.EntryModeLines) {
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
func emitLinesFile(w io.Writer, entry discovery.Entry) error {
	openFinish := platform.InternalBenchSpan("output.write_payload.lines.open_file")
	f, err := os.Open(entry.AbsPath)
	openFinish("err", platform.InternalBenchError(err))
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
	emitted := 0
	scanFinish := platform.InternalBenchSpan("output.write_payload.lines.scan_body",
		"sliced", platform.InternalBenchBool(sliced),
	)
	defer func() {
		scanFinish("emitted", platform.InternalBenchInt(emitted))
	}()

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
		emitted++
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

func writeSectionedOutputPlanPayload(w io.Writer, plan Plan, prefetch bool) error {
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
		case SectionKindPaths:
			for _, item := range section.items {
				if _, err := io.WriteString(w, item.relPath+"\n"); err != nil {
					return err
				}
			}
		case SectionKindFiles:
			subplan := Plan{items: section.items}
			if err := writeFileOutputPlanPayload(w, subplan, prefetch); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeFileOutputPlanPayload(w io.Writer, plan Plan, prefetch bool) error {
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

func outputSectionSeparator(prev, next SectionKind) string {
	if prev == SectionKindPaths && next == SectionKindFiles {
		return "\n"
	}
	if prev == SectionKindFiles && next == SectionKindPaths {
		return ""
	}
	if prev != next {
		return "\n"
	}
	return ""
}

func emitEntry(w io.Writer, unit PreparedFileUnit, index int, prefetcher *emitPrefetcher) error {
	finish := platform.InternalBenchSpan("output.write_payload.emit_entry",
		"mode", emitEntryMode(unit),
	)
	err := emitEntryInner(w, unit, index, prefetcher)
	finish("err", platform.InternalBenchError(err))
	return err
}

func emitEntryInner(w io.Writer, unit PreparedFileUnit, index int, prefetcher *emitPrefetcher) error {
	if len(unit.Payload) > 0 {
		_, err := w.Write(unit.Payload)
		return err
	}
	if len(unit.SnippetRanges) > 0 {
		return emitSnippetRangesFile(w, unit.Entry, unit.SnippetRanges)
	}
	return emitFile(w, unit, index, prefetcher)
}

func emitEntryMode(unit PreparedFileUnit) string {
	if len(unit.Payload) > 0 {
		return "prepared"
	}
	if len(unit.SnippetRanges) > 0 {
		return "snippet"
	}
	if unit.Entry.Lines {
		return "lines"
	}
	return "file"
}

func emitSnippetRangesFile(w io.Writer, entry discovery.Entry, ranges []SnippetRange) error {
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

func emitFile(w io.Writer, unit PreparedFileUnit, index int, prefetcher *emitPrefetcher) error {
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

	return EmitWrappedReader(w, relPath, typeAttr, f)
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
	return EmitWrappedReader(w, relPath, typeAttr, bytes.NewReader(data))
}

func EmitWrappedReader(w io.Writer, relPath, typeAttr string, r io.Reader) error {
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

func startEmitPrefetch(plan Plan) *emitPrefetcher {
	workers := emitReadWorkerCount()
	capBytes := emitPrefetchFileCap()
	if workers <= 1 || capBytes <= 0 {
		return nil
	}

	type job struct {
		index int
		unit  PreparedFileUnit
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

func readPrefetchCandidate(entry discovery.Entry, capBytes int64) ([]byte, bool, error) {
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

func entryUsesFullOutput(entry discovery.Entry) bool {
	return entry.Mode == "" || entry.Mode == command.EntryModeFull
}

func unitUsesFullOutput(unit PreparedFileUnit) bool {
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

type SnippetRange struct {
	Start int
	End   int
}

func DiffEntryOutput(gitCtx git.Context, entry discovery.Entry) (string, string, bool, error) {
	if !gitCtx.Enabled {
		return "", "", false, nil
	}

	repoPath := gitCtx.ToRepoPath(entry.RelPath)
	trackedOutput, err := git.Capture(gitCtx.Root, "ls-files", "--", repoPath)
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
		diffOutput, err = git.Capture(gitCtx.Root, "diff", "--cached", "--", repoPath)
		diffType = "staged-diff"
	case entry.DiffWantUnstaged && !entry.DiffWantStaged:
		diffOutput, err = git.Capture(gitCtx.Root, "diff", "--", repoPath)
		diffType = "unstaged-diff"
	default:
		diffOutput, err = git.DiffAgainstHeadOrIndex(gitCtx, repoPath)
		diffType = "diff"
	}
	if err != nil {
		return "", "", true, err
	}
	return diffOutput, diffType, true, nil
}

func WithPayloadWriter(cfg EmitConfig, env EmitEnvironment, stdout io.Writer, colors platform.Palette, fn func(io.Writer) error) (EmitStats, error) {
	bufferSize := outputBufferSize()

	if cfg.OutputMode == command.OutputModeStdout {
		counted := &countingWriter{w: stdout}
		buffered := bufio.NewWriterSize(counted, bufferSize)
		generateStarted := time.Now()
		if err := fn(buffered); err != nil {
			return EmitStats{}, err
		}
		generateDuration := time.Since(generateStarted)
		finalizeStarted := time.Now()
		if err := buffered.Flush(); err != nil {
			return EmitStats{}, err
		}
		return EmitStats{
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
		return EmitStats{}, err
	}
	if err := buffered.Flush(); err != nil {
		return EmitStats{}, err
	}
	generateDuration := time.Since(generateStarted)
	payload := payloadBuf.Bytes()

	if len(payload) >= BundleThreshold {
		return EmitBundle(env, payload, generateDuration, colors)
	}

	return emitBufferedToTextClipboard(env, payload, generateDuration, colors)
}

func streamToTextClipboard(env EmitEnvironment, fn func(io.Writer) error, colors platform.Palette, bufferSize int) (EmitStats, error) {
	cmd, err := ClipboardCommand(env.Platform, colors)
	if err != nil {
		return EmitStats{}, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return EmitStats{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return EmitStats{}, err
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
		return EmitStats{}, writeErr
	}
	if flushErr != nil {
		return EmitStats{}, flushErr
	}
	if closeErr != nil {
		return EmitStats{}, closeErr
	}
	if waitErr != nil {
		return EmitStats{}, clipboardWaitFailureError(cmd, stderr.String(), waitErr)
	}
	return EmitStats{
		PayloadBytes:          counted.n,
		GenerateDuration:      generateDuration,
		SinkFinalizeDuration:  finalizeDuration,
		ClipboardWaitDuration: waitDuration,
		SinkName:              filepath.Base(cmd.Path),
	}, nil
}

func emitBufferedToTextClipboard(env EmitEnvironment, payload []byte, generateDuration time.Duration, colors platform.Palette) (EmitStats, error) {
	cmd, err := ClipboardCommand(env.Platform, colors)
	if err != nil {
		return EmitStats{}, err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return EmitStats{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return EmitStats{}, err
	}

	finalizeStarted := time.Now()
	_, writeErr := stdin.Write(payload)
	closeErr := stdin.Close()
	finalizeDuration := time.Since(finalizeStarted)
	waitStarted := time.Now()
	waitErr := waitClipboardCommand(cmd, env.Platform)
	waitDuration := time.Since(waitStarted)

	if writeErr != nil {
		return EmitStats{}, writeErr
	}
	if closeErr != nil {
		return EmitStats{}, closeErr
	}
	if waitErr != nil {
		return EmitStats{}, clipboardWaitFailureError(cmd, stderr.String(), waitErr)
	}
	return EmitStats{
		PayloadBytes:          int64(len(payload)),
		GenerateDuration:      generateDuration,
		SinkFinalizeDuration:  finalizeDuration,
		ClipboardWaitDuration: waitDuration,
		SinkName:              filepath.Base(cmd.Path),
	}, nil
}

// clipboardWaitFailureError renders the user-facing error when a clipboard
// child process exited non-zero. For wl-copy specifically, the plan's
// Wayland-required wording surfaces compositor/session issues. For
// pbcopy/clip.exe and any other resident clipboard tool, the existing
// generic wording is preserved. Called from both the streaming emitter and
// emitBufferedToTextClipboard so the messages stay in lockstep.
func clipboardWaitFailureError(cmd *exec.Cmd, rawStderr string, waitErr error) error {
	tool := filepath.Base(cmd.Path)
	stderrMsg := strings.TrimSpace(rawStderr)
	if tool == "wl-copy" {
		detail := waitErr.Error()
		if stderrMsg != "" {
			detail = stderrMsg
		}
		return fmt.Errorf(
			"Error: wl-copy failed.\n\n"+
				"wl-copy could not accept the clipboard payload.\n\n"+
				"Check that your Wayland compositor/session is running correctly, or use stdout:\n"+
				"  catclip . --print\n"+
				"  catclip . --headless\n\n"+
				"Details: %s",
			detail,
		)
	}
	if stderrMsg != "" {
		return fmt.Errorf("Error: clipboard command failed: %s", stderrMsg)
	}
	return fmt.Errorf("Error: clipboard command failed: %w", waitErr)
}

func EmitBundle(env EmitEnvironment, payload []byte, generateDuration time.Duration, colors platform.Palette) (EmitStats, error) {
	dir := BundleDirForEnv(env)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return EmitStats{}, fmt.Errorf("Error: bundle directory: %w", err)
	}
	clearPriorBundles(dir)

	finalizeStarted := time.Now()
	tmpPath := BundleTempPath(dir, BundleProjectName(env.WorkingDir), time.Now())
	if err := os.WriteFile(tmpPath, payload, 0600); err != nil {
		return EmitStats{}, fmt.Errorf("Error: bundle write: %w", err)
	}
	if err := FileclipCopy(tmpPath); err != nil {
		// GNOME-below-46 Wayland: preserve the bundle file so the user can
		// drag-and-drop / manually copy it. This is the only remaining
		// bundle-preserving branch — v0.5.3's X11 case is gone (X11 is
		// blocked at startup) and unknown/displayless Linux is treated like
		// any other "no usable sink" failure.
		if errors.Is(err, fileclip.ErrLegacyGNOMEUnsupported) {
			return EmitStats{}, fmt.Errorf(
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
		// Unknown/displayless Linux requested clipboard delivery. Detected X11
		// is blocked at startup, so this is the SSH/Docker/TTY/CI path. Surface
		// the Wayland-required message instead of "clipboard command failed".
		if errors.Is(err, fileclip.ErrLinuxClipboardSessionUnsupported) {
			return EmitStats{}, errors.New(
				"Error: Clipboard output requires Wayland.\n\n" +
					"No Wayland session was detected.\n\n" +
					"Use stdout output instead:\n" +
					"  catclip . --print\n" +
					"  catclip . --headless",
			)
		}
		// fileclip.ErrToolNotFound is the "no clipboard binary on PATH"
		// sentinel. Render the same multi-distro install hint the text
		// clipboard path uses so the two sinks teach the user identically.
		// Other failures (ErrToolFailed or anything else) keep the
		// generic "clipboard command failed" framing — those are runtime
		// failures from a present tool, not a missing-tool situation.
		if errors.Is(err, fileclip.ErrToolNotFound) {
			return EmitStats{}, fmt.Errorf("Error: No clipboard tool found.\n%s", ClipboardInstallHint(env.Platform, colors))
		}
		return EmitStats{}, fmt.Errorf("Error: clipboard command failed: %w", err)
	}
	finalizeDuration := time.Since(finalizeStarted)

	return EmitStats{
		PayloadBytes:         int64(len(payload)),
		GenerateDuration:     generateDuration,
		SinkFinalizeDuration: finalizeDuration,
		SinkName:             "bundle",
		BundlePath:           tmpPath,
		Warnings:             BundleWarnings(env),
	}, nil
}

// unsupportedFileClipboardReason renders the reason clause for the bundle-
// preserving error path. Only GNOME-below-46 Wayland reaches this code now;
// the X11 case was retired in v0.6.0 (X11 is blocked at startup, so the
// bundle file is never written for an X11 invocation).
func unsupportedFileClipboardReason(err error) string {
	if errors.Is(err, fileclip.ErrLegacyGNOMEUnsupported) {
		return fmt.Sprintf(
			"GNOME below %d file-reference clipboard is not supported",
			fileclip.MinimumGNOMEFileClipboardMajor,
		)
	}
	return "file-reference clipboard is not supported"
}

func unsupportedFileClipboardRemedy(err error) string {
	if errors.Is(err, fileclip.ErrLegacyGNOMEUnsupported) {
		return fmt.Sprintf(
			"For one-step paste, upgrade to GNOME %d or newer.",
			fileclip.MinimumGNOMEFileClipboardMajor,
		)
	}
	return ""
}

func BundleWarnings(env EmitEnvironment) []string {
	if env.Platform != "linux" {
		return nil
	}
	// env.Platform == "linux" is the production caller's authoritative
	// signal; the session question is then only "Wayland or not". Use the
	// injectable detector with goos="linux" so this remains testable on
	// non-Linux dev hosts via env injection. WSL is filtered out above
	// because platform.Detect() returns "wsl", not "linux", for WSL.
	if platform.DetectLinuxSessionForEnv("linux", os.Getenv, "") != platform.LinuxSessionWayland {
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
	major, minor, ok := XdgDesktopPortalVersion()
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

func XdgDesktopPortalVersion() (int, int, bool) {
	raw := strings.TrimSpace(os.Getenv("CATCLIP_XDG_DESKTOP_PORTAL_VERSION"))
	if raw == "" {
		out, err := runXDGDesktopPortalVersionCommand()
		if err != nil {
			return 0, 0, false
		}
		raw = string(out)
	}
	return ParseMajorMinorVersion(raw)
}

func runXDGDesktopPortalVersionCommand() ([]byte, error) {
	if bin := strings.TrimSpace(os.Getenv("CATCLIP_XDG_DESKTOP_PORTAL_BIN")); bin != "" {
		return exec.Command(bin, "--version").Output()
	}
	var lastErr error
	for _, bin := range XdgDesktopPortalVersionCandidatePaths() {
		out, err := exec.Command(bin, "--version").Output()
		if err == nil {
			return out, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func XdgDesktopPortalVersionCandidatePaths() []string {
	return []string{
		"xdg-desktop-portal",
		"/usr/libexec/xdg-desktop-portal",
		"/usr/lib/xdg-desktop-portal",
		"/usr/lib/xdg-desktop-portal/xdg-desktop-portal",
	}
}

func ParseMajorMinorVersion(raw string) (int, int, bool) {
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

func clipboardCommandMayStayResident(cmd *exec.Cmd, plat string) bool {
	if plat == "macos" || plat == "wsl" {
		return false
	}
	switch filepath.Base(cmd.Path) {
	case "wl-copy":
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

func ClipboardCommand(plat string, colors platform.Palette) (*exec.Cmd, error) {
	switch plat {
	case "macos":
		if _, err := exec.LookPath("pbcopy"); err != nil {
			return nil, fmt.Errorf("Error: No clipboard tool found.\n%s", ClipboardInstallHint(plat, colors))
		}
		return exec.Command("pbcopy"), nil
	case "windows", "wsl":
		if path, err := exec.LookPath("clip.exe"); err == nil {
			return exec.Command(path), nil
		}
		if path, err := exec.LookPath("clip"); err == nil {
			return exec.Command(path), nil
		}
		return nil, fmt.Errorf("Error: No clipboard tool found.\n%s", ClipboardInstallHint(plat, colors))
	default:
		// Linux: Wayland-only. Detected X11 desktop sessions are blocked at
		// startup by main.linuxSessionGateError, so the only way ClipboardCommand
		// is called on non-Wayland Linux is the unknown/displayless case
		// (SSH, Docker, TTY, CI without a compositor). That asks for clipboard
		// output where catclip cannot deliver it; return a Wayland-required error.
		if platform.DetectLinuxSession() != platform.LinuxSessionWayland {
			return nil, errors.New(
				"Error: Clipboard output requires Wayland.\n\n" +
					"No Wayland session was detected.\n\n" +
					"Use stdout output instead:\n" +
					"  catclip . --print\n" +
					"  catclip . --headless",
			)
		}
		if _, err := exec.LookPath("wl-copy"); err != nil {
			return nil, fmt.Errorf("Error: Clipboard output requires wl-copy.\n%s", ClipboardInstallHint(plat, colors))
		}
		return exec.Command("wl-copy"), nil
	}
}

func ClipboardInstallHint(plat string, colors platform.Palette) string {
	switch plat {
	case "macos":
		return fmt.Sprintf("  %sEnsure pbcopy is in PATH (ships with macOS).%s", colors.Dim, colors.Reset)
	case "windows":
		return fmt.Sprintf("  %sEnsure clip.exe is available (ships with Windows).%s", colors.Dim, colors.Reset)
	case "wsl":
		return fmt.Sprintf("  %sEnsure clip.exe is reachable through WSL interop from the Windows host.%s", colors.Dim, colors.Reset)
	default:
		// Linux text + bundle clipboard delivery is Wayland-only. wl-clipboard
		// provides both wl-copy (writer) and wl-paste (reader); installing one
		// package covers both paths.
		return fmt.Sprintf("  Install wl-clipboard:\n    sudo apt install wl-clipboard    %s# Debian/Ubuntu%s\n    sudo pacman -S wl-clipboard      %s# Arch%s\n    sudo dnf install wl-clipboard    %s# Fedora%s",
			colors.Dim, colors.Reset, colors.Dim, colors.Reset, colors.Dim, colors.Reset)
	}
}

// formatBundleTimestamp renders the HHMMSS stamp embedded in large-copy bundle
// filenames. Dup of root date_format.go's formatBundleTimestamp — kept here
// because the rest of date_format.go is preview / human-output formatting.
func formatBundleTimestamp(now time.Time) string {
	return now.Format("150405")
}
