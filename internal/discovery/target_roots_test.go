package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRetainedTargetRootsStrictRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "roots.json")
	want := []string{"a b", "café.go", "--literal", "quote'and$sign.go"}
	if err := WriteTargetRoots(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTargetRoots(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip: %v, %v", got, err)
	}
	for _, raw := range []string{
		`{"version":2,"targets":["."]}`, `{"version":1,"targets":[]}`,
		`{"version":1,"targets":[""]}`, `{"version":1,"targets":["../escape"]}`,
		`{"version":1,"targets":["."],"extra":true}`, `{"version":1,"targets":["."]} {}`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadTargetRoots(path); err == nil {
			t.Fatalf("accepted invalid descriptor: %s", raw)
		}
	}
}
