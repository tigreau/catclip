package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoIgnoreInventorySharesTextAndBinaryCache(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"text.audit": "text", "binary.audit": "\x00"} {
		if err := os.WriteFile(filepath.Join(root, "src", name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(t.TempDir(), "classification.log")
	t.Setenv("CATCLIP_INTERNAL_BENCH_LOG", log)
	var walks int
	restore := SetMembershipEnumerationObserver(func(MembershipEnumerationEvent) { walks++ })
	defer restore()
	paths := []string{"src/text.audit", "src/binary.audit"}
	set, err := ResolveTextFileSetFromNoIgnoreInventory(root, []string{"src"}, paths)
	if err != nil || len(set) != 1 {
		t.Fatalf("set=%v err=%v", set, err)
	}
	text, binary, err := ResolveTextFileSetWithBinaryEvidence(root, []string{"src/", "src"})
	if err != nil || len(text) != 1 || len(binary) != 1 {
		t.Fatalf("text=%v binary=%v err=%v", text, binary, err)
	}
	if _, ok := binary["src/binary.audit"]; !ok {
		t.Fatal("lost binary evidence")
	}
	if _, err := ResolveTextFileSetFromNoIgnoreInventory(root, []string{"src"}, paths); err != nil {
		t.Fatal(err)
	}
	if walks != 0 {
		t.Fatalf("cache reuse enumerated %d times", walks)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `event="search.classify.paths"`) != 1 {
		t.Fatalf("reclassified cached inventory: %s", data)
	}
}

func TestEmptyNoIgnoreInventoryDoesNotEnumerate(t *testing.T) {
	root := t.TempDir()
	var walks int
	restore := SetMembershipEnumerationObserver(func(MembershipEnumerationEvent) { walks++ })
	defer restore()
	set, err := ResolveTextFileSetFromNoIgnoreInventory(root, nil, nil)
	if err != nil || len(set) != 0 || walks != 0 {
		t.Fatalf("set=%v err=%v walks=%d", set, err, walks)
	}
}

func TestCancelledNoIgnoreInventoryDoesNotPopulateCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "text.audit"), []byte("text"), 0600); err != nil {
		t.Fatal(err)
	}
	saved := reloadCancelCtx
	defer func() { reloadCancelCtx = saved }()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	reloadCancelCtx = cancelled
	if _, err := ResolveTextFileSetFromNoIgnoreInventory(root, nil, []string{"text.audit"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	reloadCancelCtx = saved
	set, err := ResolveTextFileSetFromNoIgnoreInventory(root, nil, []string{"text.audit"})
	if err != nil || len(set) != 1 {
		t.Fatalf("cancelled cache poisoned retry: set=%v err=%v", set, err)
	}
}
