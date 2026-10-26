package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tigreau/catclip/internal/search"
)

func TestFindIgnoredDotfilesRealIgnoreRules(t *testing.T) {
	t.Setenv("RIPGREP_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, ok := search.RipgrepBinary(); !ok {
		t.Fatal("bundled rg required")
	}
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(GlobalHissPath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(GlobalHissPath(), []byte(".env\n*.key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write(".gitignore", "git-only/**/.local\nancestor/\n")
	write("hiss/deep/.env", "synthetic=yes\n")
	write("hiss/deep/.key", "\x00synthetic binary\n")
	write("visible/.env.local", "synthetic=yes\n")
	write("visible/.local", "synthetic=yes\n")
	write("git-only/deep/.local", "synthetic=yes\n")
	write("ancestor/deep/.local", "synthetic=yes\n")
	write("nested/.gitignore", ".secret\n!keep/.secret\n")
	write("nested/hidden/.secret", "synthetic=yes\n")
	write("nested/keep/.secret", "synthetic=yes\n")
	for _, tc := range []struct {
		name  string
		want  []string
		walks int
	}{
		{".env", []string{"hiss/deep/.env"}, 2},
		{".key", []string{"hiss/deep/.key"}, 2},
		{".local", []string{"ancestor/deep/.local", "git-only/deep/.local"}, 2},
		{".secret", []string{"nested/hidden/.secret"}, 2},
		{".env.local", nil, 2},
		{".absent", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []search.MembershipEnumerationEvent
			defer search.SetMembershipEnumerationObserver(func(event search.MembershipEnumerationEvent) { events = append(events, event) })()
			got := FindIgnoredDotfiles(root, tc.name)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("paths=%q want %q", got, tc.want)
			}
			if len(events) != tc.walks {
				t.Fatalf("walks=%d want %d", len(events), tc.walks)
			}
		})
	}
}

func TestFindIgnoredDotfilesFallbackAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name       string
		budget     time.Duration
		failure    int
		candidates []string
		hissError  bool
		walks      int
	}{
		{"missing", time.Second, 0, nil, false, 1},
		{"first error", time.Second, 1, nil, false, 1},
		{"visibility error", time.Second, 2, []string{"a/.env"}, false, 2},
		{"unreadable hiss", time.Second, 0, []string{"a/.env"}, true, 1},
		{"expired", -time.Second, 0, nil, false, 0},
		{"not exact basename", time.Second, 0, []string{"a/.env.local"}, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var firstTimeout time.Duration
			got := findIgnoredDotfiles("root", ".env", tc.budget, func(_ string, opts search.RipgrepFileOptions) ([]string, error) {
				calls++
				if opts.Timeout <= 0 || opts.Timeout > tc.budget {
					t.Fatalf("invalid timeout %v", opts.Timeout)
				}
				if calls == 1 {
					firstTimeout = opts.Timeout
					if !opts.NoIgnore || !reflect.DeepEqual(opts.Basenames, []string{".env"}) {
						t.Fatalf("lookup opts=%+v", opts)
					}
				} else if opts.NoIgnore || len(opts.Basenames) > 0 || opts.Timeout > firstTimeout {
					t.Fatalf("visibility opts=%+v", opts)
				}
				if calls == tc.failure {
					return nil, context.DeadlineExceeded
				}
				return tc.candidates, nil
			}, func() (string, error) {
				if tc.hissError {
					return "", errors.New("unreadable")
				}
				return "hiss", nil
			})
			if len(got) != 0 || calls != tc.walks {
				t.Fatalf("got=%q calls=%d", got, calls)
			}
		})
	}
}
