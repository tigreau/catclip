package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInternalBenchLogWritesQuotedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catclip-bench.log")
	t.Setenv(internalBenchLogEnv, path)

	InternalBenchLog("test.event", "bad key", "value with spaces", "count", InternalBenchInt(3))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bench log: %v", err)
	}
	line := string(data)
	for _, want := range []string{
		`event="test.event"`,
		`bad_key="value with spaces"`,
		`count="3"`,
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("bench log missing %q in %q", want, line)
		}
	}
}

func TestInternalBenchSpanNoopsWhenDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catclip-bench.log")
	t.Setenv(internalBenchLogEnv, "")

	finish := InternalBenchSpan("test.span")
	time.Sleep(time.Nanosecond)
	finish("err", "false")

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("disabled bench span created log: %v", err)
	}
}
