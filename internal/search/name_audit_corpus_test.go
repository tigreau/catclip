package search

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var auditedTextBasenames = []string{"gradlew", ".prettierignore", "kconfig", "kbuild", ".kunitconfig"}

func TestAuditedTextBasenames(t *testing.T) {
	for _, name := range auditedTextBasenames {
		for _, p := range []string{name, "nested/" + name, "nested/" + strings.ToUpper(name)} {
			if classifyPathByName(p) != nameClassText {
				t.Errorf("%q should be known text", p)
			}
		}
	}
	for _, name := range []string{"config", "settings", "platform", "gradlew-copy", "Kconfig.custom", ".prettierignore.backup"} {
		if classifyPathByName(name) != nameClassUnknown {
			t.Errorf("broad admission: %q", name)
		}
	}
}

func TestUnknownNameCorpusAudit(t *testing.T) {
	if os.Getenv("CATCLIP_RUN_CORPUS_TESTS") != "1" {
		t.Skip("opt-in full corpus")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	corpus := filepath.Join(home, "Desktop", "catclip-test-data")
	paths, err := RunRipgrepFiles(corpus, RipgrepFileOptions{NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	type count struct {
		name          string
		text, nontext int
	}
	groups := make(map[string]*count)
	var unknown []string
	admitted := make(map[string]bool)
	for _, name := range auditedTextBasenames {
		admitted[name] = true
	}
	newlyKnown := 0
	for _, p := range paths {
		candidate := admitted[strings.ToLower(path.Base(p))]
		if candidate {
			newlyKnown++
		}
		// Keep auditing the newly whitelisted names against rg after admission.
		if classifyPathByName(p) == nameClassUnknown || candidate {
			unknown = append(unknown, p)
		}
	}
	textPaths, err := runRipgrepNulScanFiles(corpus, unknown)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range unknown {
		base := strings.ToLower(path.Base(p))
		if admitted[base] {
			if _, ok := textPaths[p]; !ok {
				t.Errorf("audited name is not confirmed text: %s", p)
			}
		}
		if shellStyleExtension(base) != "" {
			continue
		}
		if groups[base] == nil {
			groups[base] = &count{name: base}
		}
		if _, ok := textPaths[p]; ok {
			groups[base].text++
		} else {
			groups[base].nontext++
		}
	}
	var ranked []*count
	for _, c := range groups {
		ranked = append(ranked, c)
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i].text+ranked[i].nontext, ranked[j].text+ranked[j].nontext
		if a == b {
			return ranked[i].name < ranked[j].name
		}
		return a > b
	})
	t.Logf("enumerated=%d previous_residue=%d new_residue=%d newly_known=%d", len(paths), len(unknown), len(unknown)-newlyKnown, newlyKnown)
	for _, name := range auditedTextBasenames {
		if c := groups[name]; c != nil {
			t.Logf("admitted %q text=%d nontext=%d", name, c.text, c.nontext)
		} else {
			t.Logf("admitted %q absent from corpus", name)
		}
	}
	for i, c := range ranked {
		if i >= 65 {
			break
		}
		t.Logf("%q text=%d nontext_or_unconfirmed=%d", c.name, c.text, c.nontext)
	}
}
