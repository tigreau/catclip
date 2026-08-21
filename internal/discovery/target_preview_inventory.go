package discovery

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/git"
)

const targetPreviewInventoryMagic = "CCTPINV2"

const (
	targetPreviewIgnored = 1 << iota
	targetPreviewHiss
	targetPreviewGitEnabled
	targetPreviewGitHasHead
	targetPreviewSizeKnown
	targetPreviewSizesPending
)

// TargetPreviewInventory is the compact, immutable file universe shared by a
// target picker and its short-lived preview children. It intentionally stores
// only data the target-only tree can consume.
type TargetPreviewInventory struct {
	GitContext git.Context
	Entries    []Entry
	// SizesPending means the parent picker is still completing its shared size
	// snapshot. A large preview may wait for the completed sidecar instead of
	// duplicating the same Lstat pass in another process.
	SizesPending bool
}

type TargetPreviewInventoryWriteOptions struct {
	SizesPending bool
}

// WriteTargetPreviewInventory writes the target picker's already-classified
// file rows. Paths are length-prefixed so every legal path except NUL (which
// filesystems already reject) round-trips without shell or line parsing.
func WriteTargetPreviewInventory(path string, gitCtx git.Context, matches []TargetMatch) error {
	return WriteTargetPreviewInventoryWithOptions(path, gitCtx, matches, TargetPreviewInventoryWriteOptions{})
}

func WriteTargetPreviewInventoryWithOptions(path string, gitCtx git.Context, matches []TargetMatch, opts TargetPreviewInventoryWriteOptions) error {
	files := make([]TargetMatch, 0, len(matches))
	for _, match := range matches {
		if match.Kind == treeTargetKindFile {
			match.Path = normalizeRelPath(match.Path)
			files = append(files, match)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 256*1024)
	writeErr := writeTargetPreviewInventory(bw, gitCtx, files, opts)
	if writeErr == nil {
		writeErr = bw.Flush()
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(tmp)
		return writeErr
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func writeTargetPreviewInventory(w io.Writer, gitCtx git.Context, files []TargetMatch, opts TargetPreviewInventoryWriteOptions) error {
	if _, err := io.WriteString(w, targetPreviewInventoryMagic); err != nil {
		return err
	}
	flags := byte(0)
	if gitCtx.Enabled {
		flags |= targetPreviewGitEnabled
	}
	if gitCtx.HasHead {
		flags |= targetPreviewGitHasHead
	}
	if opts.SizesPending {
		flags |= targetPreviewSizesPending
	}
	if _, err := w.Write([]byte{flags}); err != nil {
		return err
	}
	if err := writeTargetPreviewString(w, gitCtx.Root); err != nil {
		return err
	}
	if err := writeTargetPreviewString(w, gitCtx.WorkPrefix); err != nil {
		return err
	}
	if err := writeTargetPreviewUint(w, uint64(len(files))); err != nil {
		return err
	}
	for _, match := range files {
		entryFlags := byte(0)
		if match.Ignored {
			entryFlags |= targetPreviewIgnored
			if match.IgnoreSource == ".hiss" {
				entryFlags |= targetPreviewHiss
			}
		}
		if match.SizeKnown {
			entryFlags |= targetPreviewSizeKnown
		}
		if _, err := w.Write([]byte{entryFlags}); err != nil {
			return err
		}
		if err := writeTargetPreviewString(w, normalizeRelPath(match.Path)); err != nil {
			return err
		}
		if match.SizeKnown {
			if err := writeTargetPreviewUint(w, uint64(match.SizeBytes)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeTargetPreviewString(w io.Writer, value string) error {
	if err := writeTargetPreviewUint(w, uint64(len(value))); err != nil {
		return err
	}
	_, err := io.WriteString(w, value)
	return err
}

func writeTargetPreviewUint(w io.Writer, value uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], value)
	_, err := w.Write(buf[:n])
	return err
}

// ReadTargetPreviewInventory loads the parent snapshot and restores the
// runtime-only absolute paths needed by size collection.
func ReadTargetPreviewInventory(path, workingDir string) (TargetPreviewInventory, error) {
	f, err := os.Open(path)
	if err != nil {
		return TargetPreviewInventory{}, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 256*1024)

	magic := make([]byte, len(targetPreviewInventoryMagic))
	if _, err := io.ReadFull(br, magic); err != nil {
		return TargetPreviewInventory{}, err
	}
	if string(magic) != targetPreviewInventoryMagic {
		return TargetPreviewInventory{}, fmt.Errorf("unsupported target preview inventory")
	}
	flags, err := br.ReadByte()
	if err != nil {
		return TargetPreviewInventory{}, err
	}
	root, err := readTargetPreviewString(br)
	if err != nil {
		return TargetPreviewInventory{}, err
	}
	prefix, err := readTargetPreviewString(br)
	if err != nil {
		return TargetPreviewInventory{}, err
	}
	count, err := binary.ReadUvarint(br)
	if err != nil {
		return TargetPreviewInventory{}, err
	}
	if count > uint64(^uint(0)>>1) {
		return TargetPreviewInventory{}, fmt.Errorf("target preview inventory is too large")
	}
	entries := make([]Entry, 0, int(count))
	previousPath := ""
	for range count {
		entryFlags, err := br.ReadByte()
		if err != nil {
			return TargetPreviewInventory{}, err
		}
		rel, err := readTargetPreviewString(br)
		if err != nil {
			return TargetPreviewInventory{}, err
		}
		if rel == "" || rel == "." {
			return TargetPreviewInventory{}, fmt.Errorf("target preview inventory contains an empty path")
		}
		if previousPath != "" && rel <= previousPath {
			return TargetPreviewInventory{}, fmt.Errorf("target preview inventory paths are not strictly sorted")
		}
		previousPath = rel
		ignored := entryFlags&targetPreviewIgnored != 0
		sizeKnown := entryFlags&targetPreviewSizeKnown != 0
		var sizeBytes int64
		if sizeKnown {
			size, err := binary.ReadUvarint(br)
			if err != nil {
				return TargetPreviewInventory{}, err
			}
			if size > uint64(^uint64(0)>>1) {
				return TargetPreviewInventory{}, fmt.Errorf("target preview inventory contains an invalid size")
			}
			sizeBytes = int64(size)
		}
		blockSource := ""
		if ignored {
			blockSource = ".gitignore"
			if entryFlags&targetPreviewHiss != 0 {
				blockSource = ".hiss"
			}
		}
		entries = append(entries, Entry{
			AbsPath:        filepath.Join(workingDir, filepath.FromSlash(rel)),
			RelPath:        rel,
			GitVisible:     !ignored,
			Mode:           command.EntryModeFull,
			IgnoreBypassed: ignored,
			BlockSource:    blockSource,
			SizeBytes:      sizeBytes,
			SizeKnown:      sizeKnown,
		})
	}
	if _, err := br.ReadByte(); err != io.EOF {
		if err == nil {
			return TargetPreviewInventory{}, fmt.Errorf("target preview inventory contains trailing data")
		}
		return TargetPreviewInventory{}, err
	}
	return TargetPreviewInventory{
		GitContext: git.Context{
			Enabled:    flags&targetPreviewGitEnabled != 0,
			Root:       root,
			WorkPrefix: prefix,
			HasHead:    flags&targetPreviewGitHasHead != 0,
		},
		Entries:      entries,
		SizesPending: flags&targetPreviewSizesPending != 0,
	}, nil
}

// TargetPreviewSizedInventoryPath is written once the picker-wide background
// size capture finishes. It is separate from the base inventory because
// replacing an open file is not portable to Windows.
func TargetPreviewSizedInventoryPath(basePath string) string {
	return basePath + ".sized"
}

func TargetPreviewSizedInventoryDonePath(basePath string) string {
	return basePath + ".sizes-done"
}

// WaitForTargetPreviewSizedInventory waits for either the completed size
// snapshot or its done marker. The marker lets a write failure fall back to the
// base inventory rather than leaving a preview child blocked indefinitely.
func WaitForTargetPreviewSizedInventory(ctx context.Context, basePath, workingDir string) (TargetPreviewInventory, bool, error) {
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		inventory, err := ReadTargetPreviewInventory(TargetPreviewSizedInventoryPath(basePath), workingDir)
		if err == nil {
			return inventory, true, nil
		}
		if !os.IsNotExist(err) {
			return TargetPreviewInventory{}, false, err
		}
		if _, err := os.Stat(TargetPreviewSizedInventoryDonePath(basePath)); err == nil {
			return TargetPreviewInventory{}, false, nil
		} else if !os.IsNotExist(err) {
			return TargetPreviewInventory{}, false, err
		}
		select {
		case <-ctx.Done():
			return TargetPreviewInventory{}, false, ctx.Err()
		case <-ticker.C:
		}
	}
}

func ApplyTargetPreviewSizes(matches []TargetMatch, sizes map[string]int64) []TargetMatch {
	out := append([]TargetMatch(nil), matches...)
	for index := range out {
		if out[index].Kind != treeTargetKindFile {
			continue
		}
		if size, ok := sizes[out[index].Path]; ok {
			out[index].SizeBytes = size
			out[index].SizeKnown = true
		}
	}
	return out
}

func readTargetPreviewString(r *bufio.Reader) (string, error) {
	length, err := binary.ReadUvarint(r)
	if err != nil {
		return "", err
	}
	if length > 1<<30 {
		return "", fmt.Errorf("target preview inventory string is too large")
	}
	buf := make([]byte, int(length))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// SelectTargetPreviewEntries projects exact picker selections from a sorted
// inventory. Directory membership uses a path boundary, and overlapping rows
// retain the first selected target as their display root.
func SelectTargetPreviewEntries(inventory []Entry, targets []string) []Entry {
	if len(inventory) == 0 || len(targets) == 0 {
		return nil
	}
	if len(targets) == 1 {
		target := normalizeRelPath(targets[0])
		if target == "" || target == "." {
			return append([]Entry(nil), inventory...)
		}
	}
	selected := make([]Entry, 0)
	seen := make(map[string]struct{})
	appendEntry := func(entry Entry, root string) {
		if _, ok := seen[entry.RelPath]; ok {
			return
		}
		seen[entry.RelPath] = struct{}{}
		if root != "." {
			entry.TargetRoot = root
		}
		selected = append(selected, entry)
	}
	for _, raw := range targets {
		target := normalizeRelPath(raw)
		if target == "" {
			target = "."
		}
		if target == "." {
			for _, entry := range inventory {
				appendEntry(entry, ".")
			}
			continue
		}
		start := sort.Search(len(inventory), func(i int) bool {
			return inventory[i].RelPath >= target
		})
		if start < len(inventory) && inventory[start].RelPath == target {
			appendEntry(inventory[start], target)
		}
		prefix := strings.TrimSuffix(target, "/") + "/"
		start = sort.Search(len(inventory), func(i int) bool {
			return inventory[i].RelPath >= prefix
		})
		for i := start; i < len(inventory) && strings.HasPrefix(inventory[i].RelPath, prefix); i++ {
			appendEntry(inventory[i], target)
		}
	}
	return selected
}
