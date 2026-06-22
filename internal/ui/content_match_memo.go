package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
)

// contentMatchMemo is the per-session prefix-extension cache for
// the content-match-list picker. When the user types a prefix-extension
// of the previous pattern (the common case while typing — "T" → "TO" →
// "TOD"), the new match set is by definition a subset of the previous
// one: if "T" did not appear in file F, "TO" cannot appear either. The
// memo lets the next FirstMatchLinePerFile call restrict the rg scan
// to the previous result list instead of re-scanning the full candidate
// set (e.g., 6418 files on vscode-main). The v0.6.1 trace evidence
// pinned this as ~10 s of wasted work per typed session on Windows.
//
// Storage: a JSON file alongside the checkpoint
// ({tmpdir}/content-match-memo.json). The tmpdir is owned by the
// parent (fzfCheckpointContentMatchListCommand) and cleaned up when
// the picker closes, so memo lifetime = picker session lifetime.
//
// Read/write races: on macOS/Linux fzf SIGTERMs the previous child
// before spawning the next, so reads/writes are serialized. On Windows
// fzf does NOT terminate the previous child (Part Two Item 7), so two
// children can overlap. Both the read and write paths tolerate this:
// reads fall back to a full scan on parse error, and writes are
// atomic via rename. Worst case is a wasted scan, never corruption.
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
	return filepath.Join(filepath.Dir(checkpointPath), "content-match-memo.json")
}

func readContentMatchMemo(path string) (contentMatchMemo, bool) {
	if path == "" {
		return contentMatchMemo{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return contentMatchMemo{}, false
	}
	var memo contentMatchMemo
	if err := json.Unmarshal(data, &memo); err != nil {
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
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// restrictEntriesByMemo returns the subset of entries whose AbsPath is
// in the memo's prior result set, IFF the new pattern is a strict
// prefix-extension of the memo's pattern (and both are non-empty).
// Returns (filtered, true) on a memo hit; (entries, false) otherwise.
//
// The prefix-extension precondition is a sufficient correctness check:
// if line L of file F matched the previous regex P, then L can only
// match the new regex P+X if X is appended to P (literal prefix). For
// regex-aware patterns this is conservative — some regex compositions
// might also be subsets without being literal prefix-extensions — but
// the literal check is cheap and right for the common case (the user
// is typing more characters).
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
		// recompute the first-match-line column (file contents might
		// have changed on disk). Restrict the path list so rg only
		// re-scans known matches.
	}
	if len(memo.AbsPaths) == 0 {
		// Previous result was empty: prefix-extension of an empty
		// match set is also empty by definition.
		return nil, true
	}
	allowed := make(map[string]struct{}, len(memo.AbsPaths))
	for _, p := range memo.AbsPaths {
		allowed[p] = struct{}{}
	}
	out := make([]discovery.Entry, 0, len(memo.AbsPaths))
	for _, entry := range entries {
		if _, ok := allowed[filepath.Clean(entry.AbsPath)]; ok {
			out = append(out, entry)
		}
	}
	return out, true
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
