package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/platform"
)

type HelpExampleKind string

const (
	HelpExampleDeterministic HelpExampleKind = "deterministic"
	HelpExampleInteractive   HelpExampleKind = "interactive"
	HelpExampleTemplate      HelpExampleKind = "template"
	HelpExampleGit           HelpExampleKind = "git"
	HelpExampleStdin         HelpExampleKind = "stdin"
	HelpExamplePipeline      HelpExampleKind = "shell"
	HelpExampleExpectedError HelpExampleKind = "expected-error"
	HelpExampleDiscouraged   HelpExampleKind = "discouraged-valid"
	HelpExampleExternal      HelpExampleKind = "external-side-effect"
)

type HelpExampleShellDialect string

const (
	HelpExampleShellNeutral    HelpExampleShellDialect = "argv"
	HelpExampleShellPOSIX      HelpExampleShellDialect = "posix"
	HelpExampleShellMacOS      HelpExampleShellDialect = "macos"
	HelpExampleShellPowerShell HelpExampleShellDialect = "powershell"
)

type HelpExampleSurface uint8

const (
	HelpExampleShort HelpExampleSurface = 1 << iota
	HelpExampleFull
)

type HelpExample struct {
	ID        string
	Command   string
	Kind      HelpExampleKind
	Shell     HelpExampleShellDialect
	Surfaces  HelpExampleSurface
	Exemption string
}

var helpExampleRegistry = []HelpExample{
	{ID: "interactive.pick-project", Command: "catclip", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "target.all-go-double-quoted", Command: `catclip "*.go"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.direct-src-tsx-double-quoted", Command: `catclip "src/*.tsx"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.go-and-tsx", Command: "catclip '*.go' '*.tsx'", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.all-tsx", Command: "catclip '*.tsx'", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.button-glob", Command: "catclip '*Button*'", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.direct-src-tsx", Command: "catclip 'src/*.tsx'", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "interactive.pick-and-modify", Command: "catclip --", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "git.changed-diff-interactive", Command: "catclip --changed-diff", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "tool.short-help", Command: "catclip --help", Kind: HelpExampleExternal, Shell: HelpExampleShellNeutral, Exemption: "the immediate-action parser table and cross-platform CI own help execution"},
	{ID: "tool.edit-hiss", Command: "catclip --hiss", Kind: HelpExampleExternal, Shell: HelpExampleShellNeutral, Exemption: "opens an editor and writes the user's configuration"},
	{ID: "depth.project-root", Command: "catclip . --depth 1", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "depth.project-two-levels", Command: "catclip . --depth 2 --paths", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "ignore.all-src", Command: "catclip src --no-ignore", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "git.unstaged-tsx-diff", Command: "catclip . --only '*.tsx' --unstaged-diff", Kind: HelpExampleGit, Shell: HelpExampleShellNeutral},
	{ID: "paths.project", Command: "catclip . --paths", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "then.project-paths-src-bodies", Command: "catclip . --paths --then src", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "git.redundant-unstaged", Command: "catclip . --unstaged --only '*.tsx' --unstaged-diff", Kind: HelpExampleDiscouraged, Shell: HelpExampleShellNeutral},
	{ID: "target.button-fuzzy", Command: "catclip Button", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "target.button-file", Command: "catclip Button.tsx", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "template.lines-all", Command: "catclip FILE --lines", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.lines-range", Command: "catclip FILE --lines 400 450", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.raw", Command: "catclip FILE -r", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.raw-redirect", Command: "catclip FILE -r > dest", Kind: HelpExampleTemplate, Shell: HelpExampleShellPOSIX},
	{ID: "discouraged.raw-sed", Command: "catclip FILE -r | sed -n '400,450p'", Kind: HelpExampleDiscouraged, Shell: HelpExampleShellPOSIX},
	{ID: "discouraged.full-head-tail", Command: "catclip FILE | head -450 | tail -50", Kind: HelpExampleDiscouraged, Shell: HelpExampleShellPOSIX},
	{ID: "template.full", Command: "catclip TARGET", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.changed-diff", Command: "catclip TARGET --changed-diff", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.contains-paths", Command: "catclip TARGET --contains 'REGEX' --paths", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.everything-paths", Command: "catclip TARGET --no-ignore --with-binaries --paths", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.everything-headless", Command: "catclip TARGET --no-ignore --with-binaries --paths --headless", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.not-contains", Command: "catclip TARGET --not-contains 'REGEX'", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.paths", Command: "catclip TARGET --paths", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.metadata", Command: "catclip TARGET --metadata", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.metadata-print", Command: "catclip TARGET --metadata --print", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.snippet-smart", Command: "catclip TARGET --snippet 'REGEX'", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "template.snippet-context", Command: "catclip TARGET --snippet 'REGEX' 3", Kind: HelpExampleTemplate, Shell: HelpExampleShellNeutral},
	{ID: "target.btn-fuzzy", Command: "catclip btn", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "ignored.generated-target", Command: "catclip src/generated", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "ignored.generated-paths", Command: "catclip src/generated --paths", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "ignored.generated-file", Command: "catclip src/generated/client.ts -r", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "ignored.env-local", Command: "catclip .env.local -r", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "filter.exclude-handler", Command: `catclip internal --exclude "handler/"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "filter.only-handler", Command: `catclip internal --only "handler/"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "lines.handler-all", Command: "catclip internal/handler/user.go --lines", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "lines.handler-range", Command: "catclip internal/handler/user.go --lines 40 80", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.src", Command: "catclip src", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.src-plus-go-double-quoted", Command: `catclip src "*.go"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.src-plus-go", Command: "catclip src '*.go'", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.src-plus-go-filtered", Command: "catclip src '*.go' --exclude '*_test.go' --recent 5", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "interactive.modify-src", Command: "catclip src --", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "interactive.modify-src-twice", Command: "catclip src -- --", Kind: HelpExampleInteractive, Shell: HelpExampleShellNeutral},
	{ID: "git.changed-src", Command: "catclip src --changed", Kind: HelpExampleGit, Shell: HelpExampleShellNeutral},
	{ID: "contains.todo", Command: "catclip src --contains TODO", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "depth.src-top", Command: "catclip src --depth 1", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "filter.exclude-css", Command: `catclip src --exclude "*.css"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "ignore.all-src-paths", Command: "catclip src --no-ignore --paths", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "not-contains.todo", Command: "catclip src --not-contains TODO", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "filter.only-tsx-double-quoted", Command: `catclip src --only "*.tsx"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "pipeline.combined", Command: `catclip src --only "*.tsx" --exclude "*.test.tsx" --contains "Button" --recent 3`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "pipeline.only-exclude-recent", Command: `catclip src --only "*.tsx" --exclude "*.test.tsx" --recent 3`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "pipeline.only-before-recent", Command: `catclip src --only "*.tsx" --recent 10`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "pipeline.only-then-recent", Command: `catclip src --only "*.tsx" --then docs --recent 5`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "filter.only-components-path", Command: `catclip src --only "src/components/*"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "filter.only-tsx", Command: "catclip src --only '*.tsx'", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "shell.replace-continuation", Command: `catclip src --only '*.tsx' --contains 'oldName' --paths --headless \`, Kind: HelpExamplePipeline, Shell: HelpExampleShellMacOS},
	{ID: "shell.payload-size", Command: "catclip src --only '*.tsx' --headless | wc -c", Kind: HelpExamplePipeline, Shell: HelpExampleShellPOSIX},
	{ID: "shell.file-count", Command: "catclip src --only '*.tsx' --paths --headless | wc -l", Kind: HelpExamplePipeline, Shell: HelpExampleShellPOSIX},
	{ID: "shell.line-counts", Command: "catclip src --only '*.tsx' --paths --headless | xargs wc -l | sort -rn", Kind: HelpExamplePipeline, Shell: HelpExampleShellPOSIX},
	{ID: "paths.src", Command: "catclip src --paths", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "shell.open-src-files", Command: "catclip src --paths -p | xargs vim", Kind: HelpExampleExternal, Shell: HelpExampleShellPOSIX, Exemption: "the Catclip producer is executed; launching vim is an external side effect"},
	{ID: "pipeline.recent-before-only", Command: `catclip src --recent 10 --only "*.tsx"`, Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "recent.src-three", Command: "catclip src --recent 3", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "then.separate-recent", Command: "catclip src --recent 5 --then docs --recent 5", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "size.src-range", Command: "catclip src --size 0 100", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "snippet.use-auth-smart", Command: "catclip src --snippet useAuth", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "snippet.use-auth-context", Command: "catclip src --snippet useAuth 3", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "shell.snapshot", Command: "catclip src -p > snapshot.txt", Kind: HelpExamplePipeline, Shell: HelpExampleShellPOSIX},
	{ID: "discouraged.cross-target-filter", Command: `catclip src docs --only "*.tsx"`, Kind: HelpExampleDiscouraged, Shell: HelpExampleShellNeutral},
	{ID: "recent.combined-targets", Command: "catclip src docs --recent 5", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.multiple", Command: "catclip src internal docs", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.src-components", Command: "catclip src/components", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "target.button-exact-path", Command: "catclip src/components/Button.tsx", Kind: HelpExampleDeterministic, Shell: HelpExampleShellNeutral},
	{ID: "stdin.git-diff-short", Command: "git diff --name-only --relative main | catclip . --only -", Kind: HelpExampleStdin, Shell: HelpExampleShellPOSIX},
	{ID: "stdin.git-diff-headless", Command: "git diff --name-only --relative main | catclip . --only - --headless", Kind: HelpExampleStdin, Shell: HelpExampleShellPOSIX},
	{ID: "shell.vim-command-substitution", Command: "vim $(catclip src --contains TODO --paths --headless)", Kind: HelpExampleExternal, Shell: HelpExampleShellPOSIX, Exemption: "the Catclip producer is executed; launching vim is an external side effect"},
	{ID: "shell.replace-fragment", Command: "| xargs sed -i '' 's/oldName/newName/g'", Kind: HelpExamplePipeline, Shell: HelpExampleShellMacOS},
}

var helpExamplesByCommand = func() map[string]HelpExample {
	out := make(map[string]HelpExample, len(helpExampleRegistry))
	for _, example := range helpExampleRegistry {
		out[example.Command] = example
	}
	return out
}()

type helpExampleUsage struct {
	surfaces map[string]HelpExampleSurface
	unknown  map[string]HelpExampleSurface
}

func newHelpExampleUsage() *helpExampleUsage {
	return &helpExampleUsage{
		surfaces: make(map[string]HelpExampleSurface),
		unknown:  make(map[string]HelpExampleSurface),
	}
}

func helpExampleValue(value string, surface HelpExampleSurface, usage *helpExampleUsage) string {
	example, ok := helpExamplesByCommand[value]
	if !ok {
		if usage != nil {
			usage.unknown[value] |= surface
		}
		return value
	}
	if usage != nil {
		usage.surfaces[example.ID] |= surface
	}
	return example.Command
}

func RegisteredHelpExamples() ([]HelpExample, error) {
	usage := newHelpExampleUsage()
	shortHelpText("test", "~/.config/catclip/.hiss", platform.Palette{}, usage)
	fullHelpText("test", "~/.config/catclip/.hiss", platform.Palette{}, usage)

	if len(usage.unknown) > 0 {
		commands := make([]string, 0, len(usage.unknown))
		for command := range usage.unknown {
			commands = append(commands, command)
		}
		sort.Strings(commands)
		return nil, fmt.Errorf("unregistered help examples:\n  %s", strings.Join(commands, "\n  "))
	}

	seenIDs := make(map[string]string, len(helpExampleRegistry))
	seenCommands := make(map[string]string, len(helpExampleRegistry))
	out := make([]HelpExample, 0, len(helpExampleRegistry))
	for _, example := range helpExampleRegistry {
		if previous, exists := seenIDs[example.ID]; exists {
			return nil, fmt.Errorf("duplicate help example id %q for %q and %q", example.ID, previous, example.Command)
		}
		seenIDs[example.ID] = example.Command
		if previous, exists := seenCommands[example.Command]; exists {
			return nil, fmt.Errorf("duplicate help example command %q for %q and %q", example.Command, previous, example.ID)
		}
		seenCommands[example.Command] = example.ID
		if example.Kind == HelpExampleExternal && strings.TrimSpace(example.Exemption) == "" {
			return nil, fmt.Errorf("external help example %q has no execution exemption", example.ID)
		}
		surfaces := usage.surfaces[example.ID]
		if surfaces == 0 {
			return nil, fmt.Errorf("registered help example %q is not rendered", example.ID)
		}
		example.Surfaces = surfaces
		out = append(out, example)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (example HelpExample) CatclipArgs() ([]string, bool, error) {
	start := strings.Index(example.Command, "catclip")
	if start < 0 {
		return nil, false, nil
	}
	tokens, err := splitHelpExampleCommand(example.Command[start:])
	if err != nil {
		return nil, false, err
	}
	if len(tokens) == 0 || tokens[0] != "catclip" {
		return nil, false, fmt.Errorf("could not isolate catclip argv from %q", example.Command)
	}
	args := tokens[1:]
	for i, arg := range args {
		switch arg {
		case "TARGET":
			args[i] = "src"
		case "FILE":
			args[i] = "internal/handler/user.go"
		case "REGEX":
			args[i] = "useAuth"
		}
	}
	return args, true, nil
}

func splitHelpExampleCommand(command string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	quote := byte(0)
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}

	for i := 0; i < len(command); i++ {
		ch := command[i]
		if quote != 0 {
			if ch == quote {
				quote = 0
				continue
			}
			if ch == '\\' && quote == '"' && i+1 < len(command) {
				i++
				current.WriteByte(command[i])
				continue
			}
			current.WriteByte(ch)
			continue
		}

		switch ch {
		case '\'', '"':
			quote = ch
		case ' ', '\t', '\r', '\n':
			flush()
		case '|', '>', ')':
			flush()
			return tokens, nil
		case '\\':
			if i+1 == len(command) {
				flush()
				return tokens, nil
			}
			i++
			current.WriteByte(command[i])
		default:
			current.WriteByte(ch)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", command)
	}
	flush()
	return tokens, nil
}
