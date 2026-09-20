package discovery

import (
	"crypto/rand"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/tigreau/catclip/internal/command"
)

// CheckpointInventoryRef identifies one immutable session-owned inventory.
// The token detects a descriptor paired with another generation's artifact.
type CheckpointInventoryRef struct {
	Path  string `json:"path"`
	Token string `json:"token"`
	Count int    `json:"count"`
}

type checkpointInventoryDocument struct {
	Version int
	Token   string
	Entries []CheckpointEntry
}

type checkpointProjection struct {
	Mode                command.EntryMode
	SnippetPattern      string
	SnippetContextSet   bool
	SnippetContextLines int
	Lines               bool
	LinesStart          int
	LinesEnd            int
	DiffWantStaged      bool
	DiffWantUnstaged    bool
}

func projectionForEntry(e CheckpointEntry) checkpointProjection {
	return checkpointProjection{e.Mode, e.SnippetPattern, e.SnippetContextSet,
		e.SnippetContextLines, e.Lines, e.LinesStart, e.LinesEnd, e.DiffWantStaged, e.DiffWantUnstaged}
}

func (p checkpointProjection) apply(e *CheckpointEntry) {
	e.Mode, e.SnippetPattern = p.Mode, p.SnippetPattern
	e.SnippetContextSet, e.SnippetContextLines = p.SnippetContextSet, p.SnippetContextLines
	e.Lines, e.LinesStart, e.LinesEnd = p.Lines, p.LinesStart, p.LinesEnd
	e.DiffWantStaged, e.DiffWantUnstaged = p.DiffWantStaged, p.DiffWantUnstaged
	e.SnippetMatchLines = nil
}

// WriteCheckpointInventory performs no filesystem discovery or source stat.
// The caller publishes the reference only after a successful close, owns its
// directory until session end, and invalidates reuse when inventory facts change.
func WriteCheckpointInventory(path string, entries []Entry) (CheckpointInventoryRef, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return CheckpointInventoryRef{}, err
	}
	ref := CheckpointInventoryRef{path, hex.EncodeToString(token[:]), len(entries)}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return CheckpointInventoryRef{}, err
	}
	err = gob.NewEncoder(f).Encode(checkpointInventoryDocument{1, ref.Token, entriesToCheckpoint(entries)})
	closeErr := f.Close()
	if err != nil {
		return CheckpointInventoryRef{}, err
	}
	if closeErr != nil {
		return CheckpointInventoryRef{}, closeErr
	}
	return ref, nil
}

// WriteSharedCheckpoint encodes ordered IDs and state-local projection data.
// base must be the unchanged inventory represented by ref. Full replacements
// preserve unusual per-entry overrides without weakening the transport contract.
func WriteSharedCheckpoint(path string, ref CheckpointInventoryRef, base []Entry, ids []uint32, data CheckpointData) error {
	if ref.Count != len(base) || len(ids) != len(data.Entries) {
		return fmt.Errorf("shared checkpoint inventory/selection size mismatch")
	}
	doc := checkpointDocument{Version: sharedCheckpointVersion, Scope: data.Scope, GitContext: newCheckpointGit(data.GitContext),
		GitStatus: data.GitStatus, NoIgnore: data.NoIgnore, Inventory: &ref, FileIDs: ids}
	actual := entriesToCheckpoint(data.Entries)
	doc.Projection = &checkpointProjection{}
	if len(actual) > 0 {
		projection := projectionForEntry(actual[0])
		doc.Projection = &projection
	}
	for i, id := range ids {
		if uint64(id) >= uint64(len(base)) || base[id].RelPath != actual[i].RelPath {
			return fmt.Errorf("shared checkpoint invalid file ID at position %d", i)
		}
		predicted := entryToCheckpoint(base[id])
		doc.Projection.apply(&predicted)
		lines := actual[i].SnippetMatchLines
		actual[i].SnippetMatchLines = nil
		if !sameCheckpointFacts(predicted, actual[i]) {
			if doc.Replacements == nil {
				doc.Replacements = make(map[int]CheckpointEntry)
			}
			doc.Replacements[i] = actual[i]
		}
		if len(lines) > 0 {
			if doc.SnippetLines == nil {
				doc.SnippetLines = make(map[int][]int)
			}
			doc.SnippetLines[i] = lines
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = encodeCheckpointDocument(f, doc)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// Snippet lines are carried separately; output defaults have already been
// applied before comparison. Compare facts without reflection or serialization.
func sameCheckpointFacts(a, b CheckpointEntry) bool {
	return a.RelPath == b.RelPath && a.ModTime.Equal(b.ModTime) &&
		a.SizeBytes == b.SizeBytes && a.SizeKnown == b.SizeKnown &&
		a.TargetRoot == b.TargetRoot && a.GitVisible == b.GitVisible &&
		a.IgnoreBypassed == b.IgnoreBypassed && a.BlockSource == b.BlockSource &&
		projectionForEntry(a) == projectionForEntry(b)
}

func readSharedCheckpoint(doc checkpointDocument) (CheckpointData, error) {
	if doc.Inventory == nil || doc.Projection == nil || doc.Entries != nil || doc.Inventory.Token == "" {
		return CheckpointData{}, fmt.Errorf("invalid shared checkpoint descriptor")
	}
	f, err := os.Open(doc.Inventory.Path)
	if err != nil {
		return CheckpointData{}, err
	}
	defer f.Close()
	var base checkpointInventoryDocument
	dec := gob.NewDecoder(f)
	if err := dec.Decode(&base); err != nil {
		return CheckpointData{}, err
	}
	if base.Version != 1 || base.Token != doc.Inventory.Token || len(base.Entries) != doc.Inventory.Count {
		return CheckpointData{}, fmt.Errorf("shared checkpoint inventory identity mismatch")
	}
	var trailing checkpointInventoryDocument
	if err := dec.Decode(&trailing); err != io.EOF {
		return CheckpointData{}, fmt.Errorf("shared checkpoint inventory trailing data")
	}
	for i := range doc.Replacements {
		if i < 0 || i >= len(doc.FileIDs) {
			return CheckpointData{}, fmt.Errorf("invalid checkpoint replacement position")
		}
	}
	for i := range doc.SnippetLines {
		if i < 0 || i >= len(doc.FileIDs) {
			return CheckpointData{}, fmt.Errorf("invalid checkpoint snippet position")
		}
	}
	selected := make([]CheckpointEntry, len(doc.FileIDs))
	for i, id := range doc.FileIDs {
		if uint64(id) >= uint64(len(base.Entries)) {
			return CheckpointData{}, fmt.Errorf("shared checkpoint file ID out of bounds")
		}
		e := base.Entries[id]
		doc.Projection.apply(&e)
		if override, ok := doc.Replacements[i]; ok {
			if override.RelPath != e.RelPath {
				return CheckpointData{}, fmt.Errorf("shared checkpoint replacement changed file identity")
			}
			e = override
		}
		e.SnippetMatchLines = doc.SnippetLines[i]
		selected[i] = e
	}
	return CheckpointData{Scope: doc.Scope, GitContext: doc.GitContext.toGitContext(), GitStatus: cloneStringMapOrEmpty(doc.GitStatus), Entries: checkpointToEntries(selected), NoIgnore: doc.NoIgnore}, nil
}
