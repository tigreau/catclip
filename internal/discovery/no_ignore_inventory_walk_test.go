package discovery

import (
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/search"
)

func TestNoIgnoreInventoryDoesNotWalkAgainForClassification(t *testing.T) {
	root := writeNoIgnoreTargetFixture(t, map[string]string{
		".gitignore":        "hidden/\n",
		"src/text.audit":    "text",
		"hidden/text.audit": "hidden text",
		"binary/only.audit": "\x00",
	})
	var events []search.MembershipEnumerationEvent
	restore := search.SetMembershipEnumerationObserver(func(e search.MembershipEnumerationEvent) { events = append(events, e) })
	defer restore()
	r := Resolver{Cfg: command.Invocation{WorkingDir: root}, NoIgnore: true}
	rows, err := r.AllNoIgnoreTargets(nil)
	if err != nil {
		t.Fatal(err)
	}
	walks := 0
	for _, e := range events {
		if e.IgnorePolicy == search.MembershipNoIgnore {
			walks++
			if e.Context.Reason != search.MembershipReasonTargetInventory {
				t.Fatalf("extra walk: %+v", e)
			}
		}
	}
	if walks != 1 {
		t.Fatalf("no-ignore walks=%d, want 1", walks)
	}
	before := len(events)
	repeat, err := r.AllNoIgnoreTargets(nil)
	if err != nil || !reflect.DeepEqual(rows, repeat) || len(events) != before {
		t.Fatalf("repeat changed inventory or rescanned: err=%v events=%d/%d", err, len(events), before)
	}
	fresh := Resolver{Cfg: command.Invocation{WorkingDir: root}, NoIgnore: true}
	freshRows, err := fresh.AllNoIgnoreTargets(nil)
	if err != nil || !reflect.DeepEqual(rows, freshRows) {
		t.Fatalf("new resolver lost rows: err=%v", err)
	}
	for _, row := range rows {
		if row.Path == "binary" && row.State != treeTargetStateNoTextChildren {
			t.Fatalf("binary directory state=%v", row.State)
		}
		if row.Path == "hidden/text.audit" && (!row.Ignored || row.IgnoreSource != ".gitignore") {
			t.Fatalf("ignore attribution=%+v", row)
		}
	}
}
