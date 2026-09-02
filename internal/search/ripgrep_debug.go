package search

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/tigreau/catclip/internal/platform"
)

// IgnoreTraceRecord is one causal ignore decision reported by ripgrep's
// version-pinned --debug stream. Path is working-directory-relative. Source
// and Pattern come from the deciding Gitignore Glob record; Source may be
// empty for a source-less ripgrep rule.
type IgnoreTraceRecord struct {
	Path              string
	Source            string
	Pattern           string
	RuleDirectoryOnly bool
}

// IgnoreTraceCounts describes the complete diagnostic pass. Visible counts
// raw normal-ignore paths before Catclip's text classification and later
// filters; Ignored counts boundary paths pruned by ripgrep, not descendants
// beneath an ignored directory.
type IgnoreTraceCounts struct {
	Visible int
	Ignored int
}

// RunRipgrepIgnoreTrace repeats Catclip's normal filename walk with --debug
// solely to observe raw visible counts and causal ignored boundary paths. The
// callbacks run serially in the caller goroutine even though stdout and stderr
// are drained concurrently. Neither stream is membership authority.
func RunRipgrepIgnoreTrace(
	workingDir string,
	opts RipgrepFileOptions,
	onVisible func(string),
	onIgnored func(IgnoreTraceRecord),
) (counts IgnoreTraceCounts, retErr error) {
	if opts.NoIgnore {
		return counts, fmt.Errorf("ignore trace requires normal ignore policy")
	}
	ctx := reloadCancelCtx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	finishBench := platform.InternalBenchSpan("search.rg.ignore_trace",
		"paths", platform.InternalBenchInt(len(opts.Paths)),
		"has_hiss", platform.InternalBenchBool(strings.TrimSpace(opts.HissPath) != ""),
	)
	defer func() {
		finishBench(
			"err", platform.InternalBenchError(retErr),
			"visible", platform.InternalBenchInt(counts.Visible),
			"ignored", platform.InternalBenchInt(counts.Ignored),
		)
	}()

	bin, ok := RipgrepBinary()
	if !ok {
		return counts, errRipgrepUnavailable
	}
	enumeration := opts.Enumeration
	if enumeration.Reason == "" {
		enumeration.Reason = MembershipReasonMetadataIgnoreTrace
	}
	enumeration.Authority = MembershipAuthorityDiagnostic
	span := beginMembershipEnumeration(MembershipEnumerationIgnoreDebug, MembershipVisible, enumeration)
	defer func() { span.finish(counts.Visible, scanWasCancelled(ctx, retErr), retErr) }()

	cmd := exec.CommandContext(ctx, bin, ripgrepFileArgs(opts, true)...)
	cmd.Dir = workingDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return counts, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return counts, err
	}
	if err := cmd.Start(); err != nil {
		return counts, err
	}

	events := make(chan ignoreTraceStreamEvent, 128)
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		streamRipgrepVisiblePaths(stdout, events)
	}()
	go func() {
		defer readers.Done()
		streamRipgrepIgnoreRecords(stderr, events)
	}()
	go func() {
		readers.Wait()
		close(events)
	}()

	var streamErr error
	for event := range events {
		if event.err != nil {
			if streamErr == nil {
				streamErr = event.err
			}
			continue
		}
		switch event.kind {
		case ignoreTraceVisibleEvent:
			counts.Visible++
			if onVisible != nil {
				onVisible(event.path)
			}
		case ignoreTraceIgnoredEvent:
			counts.Ignored++
			if onIgnored != nil {
				onIgnored(event.record)
			}
		}
	}
	waitErr := cmd.Wait()
	if streamErr != nil {
		return counts, streamErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return counts, ctx.Err()
		}
		if exitErr, ok := waitErr.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return counts, nil
		}
		return counts, waitErr
	}
	return counts, nil
}

type ignoreTraceStreamEventKind uint8

const (
	ignoreTraceVisibleEvent ignoreTraceStreamEventKind = iota
	ignoreTraceIgnoredEvent
)

type ignoreTraceStreamEvent struct {
	kind   ignoreTraceStreamEventKind
	path   string
	record IgnoreTraceRecord
	err    error
}

func streamRipgrepVisiblePaths(r io.Reader, events chan<- ignoreTraceStreamEvent) {
	reader := bufio.NewReader(r)
	for {
		chunk, err := reader.ReadBytes(0)
		if len(chunk) > 0 {
			if chunk[len(chunk)-1] == 0 {
				chunk = chunk[:len(chunk)-1]
			}
			if rel := normalizeRelPath(string(chunk)); rel != "" && rel != "." {
				events <- ignoreTraceStreamEvent{kind: ignoreTraceVisibleEvent, path: rel}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				events <- ignoreTraceStreamEvent{err: err}
			}
			return
		}
	}
}

func streamRipgrepIgnoreRecords(r io.Reader, events chan<- ignoreTraceStreamEvent) {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadString('\n')
		if record, ok := parseRipgrepIgnoreTraceLine(line); ok {
			events <- ignoreTraceStreamEvent{kind: ignoreTraceIgnoredEvent, record: record}
		} else if isUnsupportedRipgrepIgnoreTraceLine(line) {
			events <- ignoreTraceStreamEvent{err: fmt.Errorf("unsupported ripgrep ignore debug record from bundled version")}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				events <- ignoreTraceStreamEvent{err: err}
			}
			return
		}
	}
}

func isUnsupportedRipgrepIgnoreTraceLine(line string) bool {
	return strings.Contains(line, " ignoring ") &&
		strings.Contains(line, ignoreTraceDecisionMarker) &&
		strings.Contains(line, "is_whitelist: false")
}

const ignoreTraceDecisionMarker = ": Ignore(IgnoreMatch(Gitignore(Glob {"

func parseRipgrepIgnoreTraceLine(line string) (IgnoreTraceRecord, bool) {
	ignoredAt := strings.Index(line, " ignoring ")
	if ignoredAt < 0 {
		return IgnoreTraceRecord{}, false
	}
	rest := line[ignoredAt+len(" ignoring "):]
	decisionAt := strings.Index(rest, ignoreTraceDecisionMarker)
	if decisionAt < 0 {
		return IgnoreTraceRecord{}, false
	}
	pathValue := strings.TrimSpace(rest[:decisionAt])
	if unquoted, ok := unquoteRipgrepDebugValue(pathValue); ok {
		pathValue = unquoted
	}
	pathValue = normalizeRelPath(pathValue)
	if pathValue == "" || pathValue == "." {
		return IgnoreTraceRecord{}, false
	}

	body := rest[decisionAt+len(ignoreTraceDecisionMarker):]
	if !strings.Contains(body, "is_whitelist: false") {
		return IgnoreTraceRecord{}, false
	}
	source, _ := ripgrepDebugSomeField(body, "from: Some(")
	pattern, ok := ripgrepDebugStringField(body, "original: ")
	if !ok {
		return IgnoreTraceRecord{}, false
	}
	return IgnoreTraceRecord{
		Path:              pathValue,
		Source:            source,
		Pattern:           pattern,
		RuleDirectoryOnly: strings.Contains(body, "is_only_dir: true"),
	}, true
}

func ripgrepDebugSomeField(value, marker string) (string, bool) {
	start := strings.Index(value, marker)
	if start < 0 {
		return "", false
	}
	rest := value[start+len(marker):]
	parsed, consumed, ok := consumeRipgrepDebugValue(rest)
	if !ok || consumed >= len(rest) || rest[consumed] != ')' {
		return "", false
	}
	return parsed, true
}

func ripgrepDebugStringField(value, marker string) (string, bool) {
	start := strings.Index(value, marker)
	if start < 0 {
		return "", false
	}
	parsed, _, ok := consumeRipgrepDebugValue(value[start+len(marker):])
	return parsed, ok
}

func consumeRipgrepDebugValue(value string) (string, int, bool) {
	if value == "" {
		return "", 0, false
	}
	if value[0] != '"' {
		end := strings.IndexAny(value, ",)")
		if end < 0 {
			return "", 0, false
		}
		return strings.TrimSpace(value[:end]), end, true
	}
	escaped := false
	for i := 1; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == '"':
			unquoted, err := strconv.Unquote(value[:i+1])
			if err != nil {
				return "", 0, false
			}
			return unquoted, i + 1, true
		}
	}
	return "", 0, false
}

func unquoteRipgrepDebugValue(value string) (string, bool) {
	if len(value) < 2 || value[0] != '"' {
		return "", false
	}
	unquoted, err := strconv.Unquote(value)
	return unquoted, err == nil
}
