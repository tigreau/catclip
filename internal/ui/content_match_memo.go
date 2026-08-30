package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
)

// contentMatchMemo is the per-session literal-prefix-extension cache for
// the content-match-list picker. When the user extends literal text (the
// common case while typing — "T" → "TO" → "TOD"), the new match set is a
// subset of the previous one: if "T" did not appear in file F, "TO" cannot
// appear either. Regex source needs a stronger proof and conservatively
// misses this optimization; see restrictEntriesByMemo. The memo lets the next
// FirstMatchLinePerFile call restrict the rg scan to the previous result list
// instead of re-scanning the full candidate set (e.g., 6418 files on
// vscode-main). The v0.6.1 trace evidence pinned this as ~10 s of wasted work
// per typed session on Windows.
//
// Storage: a JSON file alongside the checkpoint
// ({tmpdir}/content-match-memo.json). The tmpdir is owned by the
// parent (fzfCheckpointContentMatchListCommand) and cleaned up when
// the picker closes, so memo lifetime = picker session lifetime.
//
// Read/write races: on macOS/Linux fzf SIGTERMs the previous child before
// spawning the next, so reads/writes are serialized. On Windows fzf does NOT
// terminate the previous child (Part Two Item 7), so two children can overlap.
// Every writer therefore owns a unique temporary file. A reader sees one
// complete pattern/path pair or a cache miss; it never sees two writers sharing
// and mutating the same temporary inode.
type contentMatchMemo struct {
	Pattern  string   `json:"pattern"`
	AbsPaths []string `json:"abs_paths"`
}

// contentMatchMemoPath returns the conventional memo path next to a
// content-match-list checkpoint. The parent writes nothing here at
// picker open; the first child writes it, subsequent children read
// then rewrite. Cleanup is via the checkpoint tmpdir's RemoveAll.
func contentMatchMemoPath(checkpointPath string) string {
	if strings.TrimSpace(checkpointPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(checkpointPath), discovery.ContentMatchMemoFilename)
}

func decodeContentMatchMemo(data []byte) (contentMatchMemo, bool) {
	if len(data) == 0 {
		return contentMatchMemo{}, false
	}
	var memo contentMatchMemo
	if err := json.Unmarshal(data, &memo); err != nil {
		return contentMatchMemo{}, false
	}
	return memo, true
}

func readContentMatchMemo(path string) (contentMatchMemo, bool) {
	if path == "" {
		return contentMatchMemo{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return contentMatchMemo{}, false
	}
	return decodeContentMatchMemo(data)
}

func exactContentMatchMemo(data []byte, pattern string) (contentMatchMemo, bool) {
	memo, ok := decodeContentMatchMemo(data)
	if !ok || memo.Pattern != pattern {
		return contentMatchMemo{}, false
	}
	return memo, true
}

// writeContentMatchMemo writes atomically via rename so an in-flight
// reader cannot see a partial JSON document. Errors are intentionally
// silent — a failed write costs the next keystroke a full scan, not
// correctness.
func writeContentMatchMemo(path, pattern string, absPaths []string) {
	if path == "" {
		return
	}
	body, err := json.Marshal(contentMatchMemo{Pattern: pattern, AbsPaths: absPaths})
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmpPath, path); err == nil {
		return
	}
	// Windows does not replace an existing destination with os.Rename. A
	// remove+retry can briefly produce a cache miss, which is safe: the next
	// reload performs a full scan. Unique temporary files still guarantee that
	// any successfully published document came from exactly one writer.
	_ = os.Remove(path)
	_ = os.Rename(tmpPath, path)
}

// restrictEntriesByMemo returns the subset of entries whose AbsPath is
// in the memo's prior result set, IFF the new pattern is either identical or
// a literal-text prefix-extension of the memo's pattern (and both are
// non-empty).
// Returns (filtered, true) on a memo hit; (entries, false) otherwise.
//
// String-prefix alone is NOT sufficient for regexes: "foo|bar" starts with
// "foo" but widens the match set, and "foo*" changes the preceding token's
// quantifier. Restrict prefix extensions only while both patterns contain no
// PCRE2 metacharacters. Identical regexes may reuse the exact cached set under
// the picker's frozen-filesystem session contract.
func restrictEntriesByMemo(entries []discovery.Entry, memo contentMatchMemo, newPattern string) ([]discovery.Entry, bool) {
	if memo.Pattern == "" || newPattern == "" {
		return entries, false
	}
	if !strings.HasPrefix(newPattern, memo.Pattern) {
		return entries, false
	}
	if newPattern == memo.Pattern {
		// Same pattern — memo is exactly the prior result set. We
		// could short-circuit entirely, but rg is still needed to
		// recompute the first-match-line column. Restrict the path list
		// so rg only re-scans known matches.
	} else if !literalContentPattern(memo.Pattern) || !literalContentPattern(newPattern) {
		return entries, false
	}
	if len(memo.AbsPaths) == 0 {
		// Previous result was empty: prefix-extension of an empty
		// match set is also empty by definition.
		return nil, true
	}
	// Keys must be filepath.Clean'd too. On Windows, Clean rewrites
	// "/" → "\" (native separator); leaving the stored keys raw while
	// the lookup keys are Clean'd produces a separator mismatch and
	// the memo silently returns an empty subset. macOS/Linux dodge
	// the bug because Clean is a no-op for already-POSIX paths.
	allowed := make(map[string]struct{}, len(memo.AbsPaths))
	for _, p := range memo.AbsPaths {
		allowed[filepath.Clean(p)] = struct{}{}
	}
	out := make([]discovery.Entry, 0, len(memo.AbsPaths))
	for _, entry := range entries {
		if _, ok := allowed[filepath.Clean(entry.AbsPath)]; ok {
			out = append(out, entry)
		}
	}
	return out, true
}

func literalContentPattern(pattern string) bool {
	return !strings.ContainsAny(pattern, `\.^$|?*+()[]{}`)
}

// matchedAbsPathsFromRows pulls the AbsPath for each row by RelPath
// lookup, so writeContentMatchMemo can store the result set for the
// next keystroke to restrict against.
func matchedAbsPathsFromRows(rows []contentMatchRow, entries []discovery.Entry) []string {
	if len(rows) == 0 {
		return nil
	}
	absByRel := make(map[string]string, len(entries))
	for _, entry := range entries {
		rel := normalizeRelPath(entry.RelPath)
		if rel == "" || strings.TrimSpace(entry.AbsPath) == "" {
			continue
		}
		if _, dup := absByRel[rel]; dup {
			continue
		}
		absByRel[rel] = filepath.Clean(entry.AbsPath)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if abs := absByRel[row.RelPath]; abs != "" {
			out = append(out, abs)
		}
	}
	return out
}
