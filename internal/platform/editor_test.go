package platform

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestResolveEditorCommandForWindowsDefaultsToNotepad(t *testing.T) {
	cmd, err := resolveEditorCommandForGOOS("windows", "", "", func(name string) (string, error) {
		if strings.EqualFold(name, "notepad.exe") {
			return `C:\Windows\System32\notepad.exe`, nil
		}
		return "", exec.ErrNotFound
	})
	if err != nil {
		t.Fatalf("resolveEditorCommandForGOOS returned error: %v", err)
	}
	if cmd.Path != `C:\Windows\System32\notepad.exe` {
		t.Fatalf("expected notepad path, got %q", cmd.Path)
	}
	if len(cmd.Args) != 0 {
		t.Fatalf("expected no args, got %#v", cmd.Args)
	}
}

func TestResolveEditorCommandForWindowsParsesQuotedEditorPath(t *testing.T) {
	editorPath := `C:\Program Files\Notepad++\notepad++.exe`
	cmd, err := resolveEditorCommandForGOOS("windows", "", `"`+editorPath+`" -multiInst --wait`, func(name string) (string, error) {
		if name == editorPath {
			return editorPath, nil
		}
		return "", exec.ErrNotFound
	})
	if err != nil {
		t.Fatalf("resolveEditorCommandForGOOS returned error: %v", err)
	}
	if cmd.Path != editorPath {
		t.Fatalf("expected %q, got %q", editorPath, cmd.Path)
	}
	if want := []string{"-multiInst", "--wait"}; !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("expected args %#v, got %#v", want, cmd.Args)
	}
}

func TestResolveEditorCommandForWindowsFallsBackToNotepad(t *testing.T) {
	cmd, err := resolveEditorCommandForGOOS("windows", "", "missing-editor --wait", func(name string) (string, error) {
		if strings.EqualFold(name, "notepad.exe") {
			return `C:\Windows\System32\notepad.exe`, nil
		}
		return "", exec.ErrNotFound
	})
	if err != nil {
		t.Fatalf("resolveEditorCommandForGOOS returned error: %v", err)
	}
	if cmd.Path != `C:\Windows\System32\notepad.exe` {
		t.Fatalf("expected notepad fallback, got %q", cmd.Path)
	}
	if len(cmd.Args) != 0 {
		t.Fatalf("expected fallback to clear args, got %#v", cmd.Args)
	}
}

func TestResolveEditorCommandForWindowsRejectsMalformedEditorCommand(t *testing.T) {
	_, err := resolveEditorCommandForGOOS("windows", "", `"C:\Program Files\Notepad++\notepad++.exe --wait`, func(name string) (string, error) {
		return "", exec.ErrNotFound
	})
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !strings.Contains(err.Error(), "$EDITOR") {
		t.Fatalf("expected error to mention $EDITOR, got %q", err)
	}
}
