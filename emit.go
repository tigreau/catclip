package catclip

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
func emitFullOutput(cfg runConfig, units []preparedFileUnit, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return emitOutputPlan(cfg, buildOutputPlan(units), stdout, colors)
}

func emitOutputPlan(cfg runConfig, plan outputPlan, stdout io.Writer, colors colorPalette) (emitStats, error) {
	if cfg.Raw {
		return emitRawOutputPlan(cfg, plan, stdout, colors)
	}
	if plan.HasPaths() {
		return emitSectionedOutputPlan(cfg, plan, stdout, colors)
	}
	return withPayloadWriter(cfg, stdout, colors, func(w io.Writer) error {
		prefetcher := startEmitPrefetch(plan)
		if prefetcher != nil {
			defer prefetcher.Close()
		}
		for i, item := range plan.items {
			if err := emitEntry(w, item.unit, i, prefetcher); err != nil {
				return err
			}
		}
		return nil
	})
}

func emitRawOutputPlan(cfg runConfig, plan outputPlan, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return withPayloadWriter(cfg, stdout, colors, func(w io.Writer) error {
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
	})
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
func emitLinesFile(w io.Writer, entry fileEntry) error {
	f, err := os.Open(entry.AbsPath)
	if err != nil {
		return err
	}
	defer f.Close()

	linesAttr := ""
	if entry.LinesStart > 0 {
		if entry.LinesEnd > 0 {
			linesAttr = fmt.Sprintf("%d-%d", entry.LinesStart, entry.LinesEnd)
		} else {
			linesAttr = fmt.Sprintf("%d-", entry.LinesStart)
		}
	}

	if _, err := w.Write(buildFileOpenTagWithLines(entry.RelPath, "", linesAttr)); err != nil {
		return err
	}

	var contentWriter io.Writer
	startLine := 1
	if entry.LinesStart > 0 {
		startLine = entry.LinesStart
	}
	nw := &numberedWriter{w: w, line: startLine, atLineStart: true}
	contentWriter = nw

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, readBufferSize()), 10*1024*1024)
	lineNum := 0
	var lastByte byte
	wroteAny := false

	for scanner.Scan() {
		lineNum++
		if entry.LinesStart > 0 && lineNum < entry.LinesStart {
			continue
		}
		if entry.LinesEnd > 0 && lineNum > entry.LinesEnd {
			break
		}
		line := scanner.Bytes()
		if _, err := contentWriter.Write(line); err != nil {
			return fmt.Errorf("failed while writing %s: %w", entry.RelPath, err)
		}
		if _, err := contentWriter.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("failed while writing %s: %w", entry.RelPath, err)
		}
		wroteAny = true
		lastByte = '\n'
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed while streaming %s: %w", entry.AbsPath, err)
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

func emitSectionedOutputPlan(cfg runConfig, plan outputPlan, stdout io.Writer, colors colorPalette) (emitStats, error) {
	return withPayloadWriter(cfg, stdout, colors, func(w io.Writer) error {
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
				prefetcher := startEmitPrefetch(subplan)
				for idx, item := range section.items {
					if err := emitEntry(w, item.unit, idx, prefetcher); err != nil {
						if prefetcher != nil {
							prefetcher.Close()
						}
						return err
					}
				}
				if prefetcher != nil {
					prefetcher.Close()
				}
			}
		}
		return nil
	})
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
	return emitFile(w, unit, index, prefetcher)
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
	info, err := os.Stat(entry.AbsPath)
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, nil
	}
	if size := info.Size(); size < 0 || size > capBytes {
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

// emitSnippetEntry writes only the blank-line-bounded blocks that matched the
// scope's --contains pattern.
func emitSnippetEntry(w io.Writer, entry fileEntry) error {
	payload, _, err := buildPreparedSnippetPayload(entry)
	if err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
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

func withPayloadWriter(cfg runConfig, stdout io.Writer, colors colorPalette, fn func(io.Writer) error) (emitStats, error) {
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

	// Clipboard mode streams directly into the platform tool. That keeps the Go
	// path closer to the shell's "generate once, send to sink" behavior and
	// avoids buffering a second full copy of the payload in memory.
	cmd, err := clipboardCommand(cfg.Platform, colors)
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
	waitErr := waitClipboardCommand(cmd, cfg.Platform)
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
	case "wl-copy", "xclip", "xsel":
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
		if _, err := exec.LookPath("xsel"); err == nil {
			return exec.Command("xsel", "--clipboard", "--input"), nil
		}
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
		if isWaylandSession() {
			return fmt.Sprintf("  Wayland detected. Install wl-clipboard:\n    sudo apt install wl-clipboard    %s# Debian/Ubuntu%s\n    sudo pacman -S wl-clipboard      %s# Arch%s",
				colors.Dim, colors.Reset, colors.Dim, colors.Reset)
		}
		return fmt.Sprintf("  X11 detected. Install xclip or xsel:\n    sudo apt install xclip xsel      %s# Debian/Ubuntu%s\n    sudo pacman -S xclip xsel        %s# Arch%s",
			colors.Dim, colors.Reset, colors.Dim, colors.Reset)
	}
}
