package platform

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

const internalBenchLogEnv = "CATCLIP_INTERNAL_BENCH_LOG"

// InternalBenchEnabled reports whether the local interactive diagnostics log is
// enabled. This is intentionally an opt-in developer tool, not product
// telemetry: catclip never writes this log unless the user/agent sets
// CATCLIP_INTERNAL_BENCH_LOG to a path.
//
// The original use case is Windows interactive latency investigation. Use a
// real picker entrypoint such as `catclip -- --`, then choose --snippet or
// --lines from the modifier menu. fzf repeatedly spawns catclip --internal-*
// helpers for content lists, previews, and picker reloads; a normal profile of
// the parent process misses those helpers, so this log gives future agents a
// process-spanning timeline without changing command behavior.
func InternalBenchEnabled() bool {
	return strings.TrimSpace(os.Getenv(internalBenchLogEnv)) != ""
}

// InternalBenchError renders an error-presence field for diagnostic logs. It
// intentionally records only true/false, not err.Error(), because these probes
// can fire for every keystroke in --snippet/--lines previews and should not
// dump user regexes, paths, or OS-specific error strings unless a future agent
// deliberately adds a narrower probe.
func InternalBenchError(err error) string {
	return strconv.FormatBool(err != nil)
}

// InternalBenchCancelled records whether an error is a known cancellation. This
// helps separate slow/aborted fzf reload helpers from real failures when reading
// a CATCLIP_INTERNAL_BENCH_LOG trace.
func InternalBenchCancelled(err, sentinel error) string {
	return strconv.FormatBool(err != nil && sentinel != nil && errors.Is(err, sentinel))
}

// InternalBenchInt formats counts without each caller importing strconv.
func InternalBenchInt(n int) string {
	return strconv.Itoa(n)
}

// InternalBenchBool formats booleans without each caller importing strconv.
func InternalBenchBool(v bool) string {
	return strconv.FormatBool(v)
}

// InternalBenchLog appends one key=value event to CATCLIP_INTERNAL_BENCH_LOG.
// Callers pass alternating key/value pairs after event. Values are quoted so
// spaces in paths or command labels stay parseable.
func InternalBenchLog(event string, fields ...string) {
	path := strings.TrimSpace(os.Getenv(internalBenchLogEnv))
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	var b strings.Builder
	fmt.Fprintf(&b, "ts=%s pid=%d event=%s", time.Now().Format(time.RFC3339Nano), os.Getpid(), strconv.Quote(event))
	for i := 0; i+1 < len(fields); i += 2 {
		key := benchFieldKey(fields[i])
		if key == "" {
			continue
		}
		fmt.Fprintf(&b, " %s=%s", key, strconv.Quote(fields[i+1]))
	}
	b.WriteByte('\n')
	_, _ = io.WriteString(f, b.String())
}

// InternalBenchSpan returns a closure that logs elapsed time for a local
// diagnostic span. The closure is intentionally cheap when disabled so leaving
// these probes near hot preview paths does not affect normal users.
func InternalBenchSpan(event string, fields ...string) func(...string) {
	if !InternalBenchEnabled() {
		return func(...string) {}
	}
	start := time.Now()
	base := append([]string(nil), fields...)
	return func(extra ...string) {
		elapsed := time.Since(start)
		all := append([]string(nil), base...)
		all = append(all,
			"elapsed", elapsed.String(),
			"elapsed_ms", fmt.Sprintf("%.3f", float64(elapsed.Microseconds())/1000),
		)
		all = append(all, extra...)
		InternalBenchLog(event, all...)
	}
}

func benchFieldKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
