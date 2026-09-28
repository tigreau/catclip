package discovery

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// A retained projection for a compact inventory, not membership authority.
type targetRootsDocument struct {
	Version int      `json:"version"`
	Targets []string `json:"targets"`
}

func WriteTargetRoots(path string, targets []string) error {
	if err := validateTargetRoots(targets); err != nil {
		return err
	}
	data, err := json.Marshal(targetRootsDocument{Version: 1, Targets: targets})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ReadTargetRoots(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var doc targetRootsDocument
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("unsupported retained target roots version %d", doc.Version)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("retained target roots contain trailing data")
	}
	if err := validateTargetRoots(doc.Targets); err != nil {
		return nil, err
	}
	return doc.Targets, nil
}

func validateTargetRoots(targets []string) error {
	if len(targets) == 0 {
		return fmt.Errorf("retained target roots are empty")
	}
	for _, target := range targets {
		if target == "" {
			return fmt.Errorf("retained target root is empty")
		}
		if err := ValidateTargetBoundary(target); err != nil {
			return err
		}
	}
	return nil
}
