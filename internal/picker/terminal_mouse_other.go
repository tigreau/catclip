//go:build !windows

package picker

func preparePickerTerminal() (func() error, error) {
	return func() error { return nil }, nil
}
