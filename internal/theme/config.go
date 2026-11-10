package theme

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type preference struct {
	Theme string `json:"theme"`
}

func ConfigPath() (string, error) {
	if root := os.Getenv("XDG_CONFIG_HOME"); root != "" {
		return filepath.Join(root, "catclip", "theme.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "catclip", "theme.json"), nil
}
func readPreference(path string) string {
	// A missing preference is normal; one open attempt also handles that case
	// without a separate existence check on every invocation.
	f, err := os.Open(path)
	if err != nil {
		return DefaultID
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	var p preference
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &p) != nil {
		return DefaultID
	}
	for _, c := range choices {
		if c.ID == p.Theme {
			return c.ID
		}
	}
	return DefaultID
}

// Save is called only after the user confirms a selection, never while browsing.
func Save(id string) error {
	if _, ok := Lookup(id); !ok {
		return fmt.Errorf("unknown theme %q", id)
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return savePreference(path, id)
}
func savePreference(path, id string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".theme-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(preference{Theme: id})
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
