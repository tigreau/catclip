package platform

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFileScanProgressRendererClearsWithoutSpam(t *testing.T) {
	var out bytes.Buffer
	renderer := fileScanProgressRenderer{output: &out}
	renderer.draw("|", "", "")
	renderer.clear()
	if out.Len() != 0 {
		t.Fatal("fast completion emitted terminal control bytes")
	}
	renderer.draw("|", "checking", "")
	renderer.draw("/", "checking", "project hint")
	renderer.draw("-", "checking again", "project hint")
	if strings.Count(out.String(), "project hint") != 1 {
		t.Fatalf("hint repeated during redraw: %q", out.String())
	}
	out.Reset()
	renderer.draw("|", "", "") // completion, cancellation, or fast next batch
	want := "\r\033[K\033[1A\r\033[K"
	if out.String() != want {
		t.Fatalf("cleanup=%q, want %q", out.String(), want)
	}
	out.Reset()
	renderer.clear()
	if out.Len() != 0 {
		t.Fatal("repeated stop moved the cursor again")
	}
}

func TestFileScanProgressPhases(t *testing.T) {
	started := time.Now()
	for _, tc := range []struct {
		name        string
		elapsed     time.Duration
		batchStart  time.Duration
		done, total int
		want        string
		hint        bool
	}{
		{name: "fast discovery", elapsed: 999 * time.Millisecond},
		{name: "slow discovery without unknown types", elapsed: time.Second, want: "Finding files…"},
		{name: "fast classification", elapsed: 999 * time.Millisecond, total: 9429},
		{name: "classification progress", elapsed: time.Second, done: 6180, total: 9429, want: "Checking unknown file types… 6,180 / 9,429"},
		{name: "slow classification", elapsed: 5 * time.Second, done: 6180, total: 9429, want: "Checking unknown file types… 6,180 / 9,429", hint: true},
		{name: "slow discovery", elapsed: 5 * time.Second, want: "Finding files…", hint: true},
		{name: "fast batch after slow walk", elapsed: 6 * time.Second, batchStart: 5500 * time.Millisecond, total: 10},
		{name: "completed batch", elapsed: 6 * time.Second, done: 10, total: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progress := FileScanProgress{Completed: tc.done, Total: tc.total, Started: started.Add(tc.batchStart)}
			message, hint := fileScanProgressText(started.Add(tc.elapsed), started, "/project", progress)
			if message != tc.want || (hint != "") != tc.hint {
				t.Fatalf("got (%q, %q), want message %q, hint %v", message, hint, tc.want, tc.hint)
			}
			if tc.hint && hint != "Is /project the project you want to scan?\nIf not, press Ctrl+C and run Catclip inside your project folder.\n" {
				t.Fatalf("unexpected hint: %q", hint)
			}
			if strings.Contains(message+hint, "cache") {
				t.Fatal("progress must not promise an unimplemented persistent cache")
			}
		})
	}
}

func TestSlowProgressAlwaysOffersCancelAndProjectGuidance(t *testing.T) {
	for _, dir := range []string{
		`C:\Users\Chris`, `C:\Users\Chris\Desktop`, `C:\Users`, `C:\`,
		`C:\Windows`, `C:\Users\Chris\Desktop\project`,
		"/", "/home", "/home/chris", "/home/chris/Desktop", "/usr", "/work/project",
		"/Users/chris", "/Users/chris/Desktop", "/System",
	} {
		for _, phase := range []string{"Finding files…", "Checking unknown file types… 10 / 100", "Preparing selected files…"} {
			_, hint := delayedProgressText(5*time.Second, dir, phase)
			for _, want := range []string{dir, "project you want to scan?", "Ctrl+C", "run Catclip inside your project folder"} {
				if !strings.Contains(hint, want) {
					t.Fatalf("%q in %q: hint %q missing %q", phase, dir, hint, want)
				}
			}
		}
	}
}

func TestSlowProgressHintColorsOnlyPathAndShortcut(t *testing.T) {
	// A path containing the shortcut text must not be styled by text replacement.
	dir := `C:\projects\Ctrl+C`
	colors := ANSIPalette()
	got := slowProgressHint(dir, colors)
	if !strings.Contains(got, colors.Bold+colors.Warn+dir+colors.Reset) ||
		!strings.Contains(got, colors.Bold+colors.Err+"Ctrl+C"+colors.Reset) {
		t.Fatalf("missing directory/shortcut colors: %q", got)
	}
	plain := strings.NewReplacer(colors.Err, "", colors.Bold, "", colors.Warn, "", colors.Reset, "").Replace(got)
	if want := slowProgressHint(dir, Palette{}); plain != want || strings.Contains(want, "\033") {
		t.Fatalf("color changed hint text: plain=%q want=%q", plain, want)
	}
}

func TestSlowProgressGuidanceDoesNotRepeatAndClearsBlankRow(t *testing.T) {
	_, hint := delayedProgressText(5*time.Second, "/project", "Finding files…")
	var out bytes.Buffer
	renderer := fileScanProgressRenderer{output: &out}
	renderer.draw("|", "Finding files…", hint)
	renderer.draw("/", "Finding files…", hint)
	if strings.Count(out.String(), "Ctrl+C") != 1 {
		t.Fatalf("repeated cancellation guidance: %q", out.String())
	}
	if !strings.Contains(out.String(), "folder.\n\n") {
		t.Fatalf("missing blank line after guidance: %q", out.String())
	}
	out.Reset()
	renderer.clear()
	if got, want := out.String(), "\r\033[K\033[1A\r\033[K\033[1A\r\033[K\033[1A\r\033[K"; got != want {
		t.Fatalf("clear=%q, want %q", got, want)
	}
}

func TestFileScanProgressNonTerminalDoesNotObserve(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "redirected")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, output := range []*os.File{nil, f} {
		stopPreparation := StartOutputPreparationProgress(output, "/project")
		stopPreparation()
		stopPreparation()
		stop := StartFileScanProgress(output, "/project", func() (func() FileScanProgress, func()) {
			t.Fatal("redirected output must not enable classifier accounting")
			return nil, nil
		})
		stop()
		stop()
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatalf("redirected progress wrote output: info=%v, err=%v", info, err)
	}
}

func TestOutputPreparationProgressDelay(t *testing.T) {
	for _, elapsed := range []time.Duration{0, 999 * time.Millisecond, time.Second, 5 * time.Second} {
		message, hint := delayedProgressText(elapsed, "/project", "Preparing selected files…")
		if elapsed < time.Second {
			if message != "" || hint != "" {
				t.Fatalf("fast preparation emitted status: %q %q", message, hint)
			}
			continue
		}
		if message != "Preparing selected files…" || (hint != "") != (elapsed >= 5*time.Second) {
			t.Fatalf("preparation at %s: %q %q", elapsed, message, hint)
		}
	}
}
