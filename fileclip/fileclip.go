package fileclip

import (
	"fmt"
	"os"
	"path/filepath"
)

// Copy places one or more files on the system clipboard as native file
// references. Pasting into Finder copies the files. Pasting into a web
// browser (Claude, ChatGPT, Gemini) attaches them as uploads.
//
// All paths are resolved to absolute paths before being placed on the
// clipboard. Symlinks are preserved (matching Finder behavior).
//
// Returns [ErrFileNotFound] if any path does not exist, and [ErrNotAFile]
// if any path points to a directory. Web UIs do not handle directory
// pastes — only regular files are accepted.
//
// Calling Copy with zero paths is a no-op and returns nil.
func Copy(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}

	absPaths := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrFileNotFound, p)
		}

		info, err := os.Lstat(abs)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrFileNotFound, abs)
		}

		// Accept regular files and symlinks (which may point to files).
		// Reject directories — web UIs cannot handle directory pastes.
		if info.IsDir() {
			return fmt.Errorf("%w: %s", ErrNotAFile, abs)
		}

		absPaths = append(absPaths, abs)
	}

	return copyPlatform(absPaths)
}

// Paste returns the file paths currently on the clipboard as file references.
// Returns [ErrNoFileRefs] if the clipboard does not contain file references.
//
// Paste reads the same clipboard type that Finder uses when you ⌘C a file.
// If the clipboard contains text (from pbcopy, etc.) instead of file
// references, Paste returns ErrNoFileRefs.
func Paste() ([]string, error) {
	return pastePlatform()
}

// Has reports whether the clipboard currently contains file references
// (as opposed to text or image data).
//
// Use Has to check the clipboard type before calling [Paste]:
//
//	if ok, _ := fileclip.Has(); ok {
//	    paths, err := fileclip.Paste()
//	    // ...
//	}
func Has() (bool, error) {
	return hasPlatform()
}
