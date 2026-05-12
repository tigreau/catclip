//go:build !darwin && !linux && !windows

package fileclip

func copyPlatform(_ []string) error {
	return ErrUnsupportedPlatform
}

func pastePlatform() ([]string, error) {
	return nil, ErrUnsupportedPlatform
}

func hasPlatform() (bool, error) {
	return false, ErrUnsupportedPlatform
}
