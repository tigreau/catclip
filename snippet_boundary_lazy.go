package catclip

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// snippetBoundarySource is the context-independent data the lazy `snippet mode>`
// boundary preview needs: the matched files (in emission order) and their match
// lines. It is computed ONCE when the picker opens and serialized to a tmpdir;
// the per-focus preview handler reads it and renders just the focused boundary
// width. This replaces pre-rendering all 8 boundary documents before the picker
// opens — match lines and snapshots are width-independent, so only the focused
// width's body reads/extraction need to happen, and only when focused.
type snippetBoundarySource struct {
	Pattern string                       `json:"pattern"`
	Entries []snippetBoundarySourceEntry `json:"entries"`
}

type snippetBoundarySourceEntry struct {
	RelPath string `json:"rel"`
	AbsPath string `json:"abs"`
	Lines   []int  `json:"lines"`
}

func writeSnippetBoundarySource(path string, source snippetBoundarySource) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(source); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func readSnippetBoundarySource(path string) (snippetBoundarySource, error) {
	f, err := os.Open(path)
	if err != nil {
		return snippetBoundarySource{}, err
	}
	defer f.Close()
	var source snippetBoundarySource
	if err := json.NewDecoder(f).Decode(&source); err != nil {
		return snippetBoundarySource{}, err
	}
	return source, nil
}

func snippetBoundaryChoiceByKey(key string) (startupSnippetBoundaryChoice, bool) {
	for _, choice := range startupSnippetBoundaryChoices {
		if choice.Key == key {
			return choice, true
		}
	}
	return startupSnippetBoundaryChoice{}, false
}

// streamSnippetBoundaryPreview emits the snippet output for a single boundary
// width by streaming each matched file's payload straight to w, one file at a
// time, flushing after each. The raw (highlight=false) payload is the same
// `<file lines>` output the commit path produces for `--snippet PATTERN N`
// (preview == what gets copied), verified byte-identical in
// TestSnippetBoundaryStreamMatchesCommit. highlight=true colorizes each file's
// body with the same chroma pass the output picker uses
// (highlightFileBlocksForSinkPreview) — a pure display layer that only adds ANSI
// inside <file> bodies, leaving the structure (and thus which files/lines appear)
// unchanged. It is cheap now only because lexer selection is memoized by type
// (internal/tree); before that cache a per-block highlight here would reintroduce
// the very cost the sink preview was suffering.
//
// Streaming is deliberately uncapped and holds at most one file's payload: unlike
// the sink picker (whose output can be many whole files at once), snippet output
// is small per file, and rendering is not the cost — eager body loading was.
// fzf's pipe backpressure bounds work to a screenful: once fzf stops reading (you
// move focus, it closes the pipe), the next Write fails and we stop, having read
// only the files fzf actually showed. Memory stays flat regardless of match count.
func streamSnippetBoundaryPreview(source snippetBoundarySource, choice startupSnippetBoundaryChoice, w io.Writer, highlight bool) error {
	opts := snippetOptionsFor(choice.SnippetContextSet, choice.SnippetContextLines)
	bw := bufio.NewWriter(w)
	for _, e := range source.Entries {
		if e.AbsPath == "" || len(e.Lines) == 0 {
			continue
		}
		snap, err := loadTextSnapshot(e.AbsPath, e.RelPath)
		if err != nil || !snap.IsText {
			continue
		}
		payload, _, _, err := buildPreparedSnippetPayloadFromLines(e.RelPath, snap.SnippetLines(), e.Lines, opts)
		if err != nil || len(payload) == 0 {
			continue
		}
		if highlight {
			// Each payload is complete <file>…</file> block(s), so the per-file
			// highlight is identical to highlighting the whole buffer at once.
			payload = highlightFileBlocksForSinkPreview(payload)
		}
		if _, werr := bw.Write(payload); werr != nil {
			return nil // fzf closed the pipe (focus moved) — stop streaming
		}
		if ferr := bw.Flush(); ferr != nil {
			return nil
		}
	}
	return bw.Flush()
}

// runInternalSnippetBoundaryPreview is the per-focus handler fzf invokes for the
// `snippet mode>` picker. It loads the source written at picker open and streams
// the focused boundary key's snippet output, syntax-highlighted to match the
// output picker's preview. No catclip-tree pipe. Unknown keys emit nothing.
func runInternalSnippetBoundaryPreview(sourcePath, key string, stdout io.Writer) error {
	source, err := readSnippetBoundarySource(sourcePath)
	if err != nil {
		return err
	}
	choice, ok := snippetBoundaryChoiceByKey(key)
	if !ok {
		return nil
	}
	return streamSnippetBoundaryPreview(source, choice, stdout, true)
}
