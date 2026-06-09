package platform

import "runtime"

// MultiSelectToggleAllBinding returns the fzf binding used for "toggle all"
// in multi-select pickers. The binding differs on macOS because Ctrl-A is
// "go to start of line" by terminal convention only off-darwin (where Alt-A
// is the consensus).
func MultiSelectToggleAllBinding() string {
	return multiSelectToggleAllBindingForGOOS(runtime.GOOS)
}

func multiSelectToggleAllBindingForGOOS(goos string) string {
	if goos == "darwin" {
		return "ctrl-a:toggle-all"
	}
	return "alt-a:toggle-all"
}

// MultiSelectToggleAllKey returns the human-readable label for the
// "toggle all" binding, suitable for header rendering.
func MultiSelectToggleAllKey() string {
	return multiSelectToggleAllKeyForGOOS(runtime.GOOS)
}

func multiSelectToggleAllKeyForGOOS(goos string) string {
	if goos == "darwin" {
		return "Ctrl-A"
	}
	return "Alt-A"
}
