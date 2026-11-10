package search

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Like content classification, access observations belong to one stable-filesystem
// CLI process. They are evidence only: callers still own selection and scope.
var accessFailures sync.Map // absolute path -> accessFailureObservation

type accessFailureObservation struct {
	Metadata bool
	Vanished bool
}

type AccessFailure struct {
	Path     string
	Metadata bool // stat/traversal failed, rather than just a body read
	Vanished bool
}

// BodyPermissionDenied distinguishes unreadable content from positive binary
// evidence. Metadata may retain such a regular file after a successful stat.
func BodyPermissionDenied(abs string) bool {
	abs, err := filepath.Abs(abs)
	if err != nil {
		return false
	}
	value, ok := accessFailures.Load(abs)
	return ok && !value.(accessFailureObservation).Metadata && !value.(accessFailureObservation).Vanished
}

// AccessFailureTag describes a failure already observed by a real operation.
// It never probes the filesystem.
func AccessFailureTag(abs string) string {
	abs, err := filepath.Abs(abs)
	if err != nil {
		return ""
	}
	value, ok := accessFailures.Load(abs)
	if !ok {
		return ""
	}
	if value.(accessFailureObservation).Vanished {
		return "no longer exists"
	}
	return "permission error"
}

// RecordAccessFailure retains actual OS failures, never guesses from mode bits.
func RecordAccessFailure(err error, metadata bool) bool {
	if err == nil {
		return false
	}
	vanished := errors.Is(err, fs.ErrNotExist)
	if !errors.Is(err, fs.ErrPermission) && !vanished {
		return false
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		return false
	}
	abs, absErr := filepath.Abs(pathErr.Path)
	if absErr != nil {
		return false
	}
	observation := accessFailureObservation{Metadata: metadata, Vanished: vanished}
	previous, loaded := accessFailures.LoadOrStore(abs, observation)
	for loaded {
		old := previous.(accessFailureObservation)
		merged := accessFailureObservation{Metadata: old.Metadata || metadata, Vanished: old.Vanished || vanished}
		if merged == old || accessFailures.CompareAndSwap(abs, old, merged) {
			break
		}
		previous, loaded = accessFailures.Load(abs)
	}
	return true
}

// AccessFailures returns a snapshot; consumers must restrict it to their
// current roots/stages and requested operation, including after picker undo.
func AccessFailures(root string) []AccessFailure {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	var paths []AccessFailure
	accessFailures.Range(func(key, value any) bool {
		rel, err := filepath.Rel(absRoot, key.(string))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			observation := value.(accessFailureObservation)
			paths = append(paths, AccessFailure{Path: filepath.ToSlash(rel), Metadata: observation.Metadata, Vanished: observation.Vanished})
		}
		return true
	})
	sort.Slice(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
	return paths
}
