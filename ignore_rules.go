package catclip

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

// --all-ignore-rules: read the rule files that decide what catclip hides,
// fold them into a deduped union (one row per unique pattern, tagged with
// every source:line origin), and print to stdout. Display, not evaluation:
// rg still owns the actual ignore decision. See
// docs/versions/v0.5.7/reports/ACTIVE_PLAN_combined_ignore_rules.md.

type listIgnoreRulesConfig struct {
	WorkingDir string
	Targets    []string
}

func listIgnoreRulesConfigFromParsedCommand(cfg command.Parsed) listIgnoreRulesConfig {
	var targets []string
	for _, scope := range cfg.Command.Scopes() {
		for _, t := range scope.Targets() {
			if t == "" {
				continue
			}
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		targets = []string{"."}
	}
	return listIgnoreRulesConfig{
		WorkingDir: cfg.WorkingDir,
		Targets:    targets,
	}
}

// ignoreSourceKind groups sources for legend rendering. Tag rows still use the
// per-source label so e.g. ./.gitignore and ./web/.gitignore appear distinctly.
type ignoreSourceKind int

const (
	sourceKindGitignoreRoot ignoreSourceKind = iota
	sourceKindGitignoreNested
	sourceKindInfoExclude
	sourceKindHiss
	sourceKindGlobalExcludes
)

// ignoreSource is one file contributing rules to the union view.
type ignoreSource struct {
	kind  ignoreSourceKind
	label string // tag/legend label: ".gitignore", "web/.gitignore", ".git/info/exclude", ".hiss", "(global)"
	path  string // absolute path to the source file
	rank  int    // precedence: higher wins
}

// ignoreRuleOccurrence is one (source, line) pairing for a pattern.
type ignoreRuleOccurrence struct {
	source *ignoreSource
	line   int
}

type ignoreRulesUnion struct {
	Patterns     []string                          // unique, sorted by pattern text
	ByPattern    map[string][]ignoreRuleOccurrence // occurrences per pattern, sorted by rank desc
	TotalRules   int                               // total occurrences (= sum over patterns)
	Contributing []*ignoreSource                   // sources that contributed ≥1 rule, sorted by rank desc
}

func runListIgnoreRules(cfg listIgnoreRulesConfig, stdout, stderr io.Writer) error {
	_ = stderr
	sources, err := locateIgnoreSources(cfg)
	if err != nil {
		return err
	}
	union, err := buildIgnoreRulesUnion(sources)
	if err != nil {
		return err
	}
	return renderIgnoreRulesUnion(stdout, union, platform.ActivePaletteForWriter(stdout))
}

// locateIgnoreSources finds the files whose rules rg actually consults under
// the given targets. Cheap first cut: under-target glob for .gitignore, plus
// the always-global sources (info/exclude, .hiss, core.excludesFile). Parent
// .gitignores above the target are not enumerated — documented in the plan.
func locateIgnoreSources(cfg listIgnoreRulesConfig) ([]*ignoreSource, error) {
	var sources []*ignoreSource
	seen := map[string]bool{}

	// .gitignore files under each target. Use the default ignore-aware listing
	// so we don't pick up e.g. node_modules/.gitignore inside ignored dirs (rg
	// wouldn't apply those to visible content either).
	for _, target := range cfg.Targets {
		rels, err := search.RunRipgrepFiles(cfg.WorkingDir, search.RipgrepFileOptions{
			Basenames:   []string{".gitignore"},
			Paths:       []string{target},
			Enumeration: search.MembershipEnumerationContext{Reason: search.MembershipReasonIgnoreRuleListing},
		})
		if err != nil {
			return nil, fmt.Errorf("locate .gitignore files under %s: %w", target, err)
		}
		for _, rel := range rels {
			abs := filepath.Clean(filepath.Join(cfg.WorkingDir, filepath.FromSlash(rel)))
			if seen[abs] {
				continue
			}
			seen[abs] = true
			relSlash := filepath.ToSlash(rel)
			depth := strings.Count(relSlash, "/")
			kind := sourceKindGitignoreRoot
			if depth > 0 {
				kind = sourceKindGitignoreNested
			}
			sources = append(sources, &ignoreSource{
				kind:  kind,
				label: relSlash,
				path:  abs,
				rank:  1000 + depth,
			})
		}
	}

	// Always include the repo-root .gitignore when in a git repo. The
	// under-target glob above only finds .gitignores *under* the target, so a
	// subdir target like `catclip cmd --all-ignore-rules` would otherwise drop
	// the root .gitignore even though rg applies it to files under cmd/.
	// (Parent .gitignores between target and repo root are not enumerated —
	// the documented "cheap first cut" limitation.)
	gitCtx := git.Detect(cfg.WorkingDir)
	if gitCtx.Enabled {
		rootGitignore := filepath.Clean(filepath.Join(gitCtx.Root, ".gitignore"))
		if fileReadable(rootGitignore) && !seen[rootGitignore] {
			seen[rootGitignore] = true
			rel, err := filepath.Rel(cfg.WorkingDir, rootGitignore)
			if err != nil || rel == "" {
				rel = ".gitignore"
			}
			label := filepath.ToSlash(rel)
			sources = append(sources, &ignoreSource{
				kind:  sourceKindGitignoreRoot,
				label: label,
				path:  rootGitignore,
				rank:  1000, // root depth
			})
		}

		// .git/info/exclude (per-repo, not shared via git).
		infoExclude := filepath.Join(gitCtx.Root, ".git", "info", "exclude")
		if fileReadable(infoExclude) && !seen[infoExclude] {
			seen[infoExclude] = true
			sources = append(sources, &ignoreSource{
				kind:  sourceKindInfoExclude,
				label: ".git/info/exclude",
				path:  infoExclude,
				rank:  500,
			})
		}
	}

	// .hiss — read-only check; do not auto-create (listing has no side effects).
	hissPath := discovery.GlobalHissPath()
	if fileReadable(hissPath) && !seen[hissPath] {
		seen[hissPath] = true
		sources = append(sources, &ignoreSource{
			kind:  sourceKindHiss,
			label: ".hiss",
			path:  hissPath,
			rank:  100,
		})
	}

	// Global core.excludesFile (machine-wide).
	if globalPath := resolveGlobalExcludesFile(cfg.WorkingDir); globalPath != "" {
		abs := filepath.Clean(globalPath)
		if fileReadable(abs) && !seen[abs] {
			seen[abs] = true
			sources = append(sources, &ignoreSource{
				kind:  sourceKindGlobalExcludes,
				label: "(global)",
				path:  abs,
				rank:  50,
			})
		}
	}

	return sources, nil
}

func resolveGlobalExcludesFile(workingDir string) string {
	out, err := git.Capture(workingDir, "config", "--get", "core.excludesFile")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if p == "" {
		return ""
	}
	// git config returns the value verbatim; expand a leading ~/ ourselves.
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			p = filepath.Join(home, p[2:])
		}
	}
	return p
}

func fileReadable(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

// buildIgnoreRulesUnion reads each source, parses its rule lines, folds
// occurrences by exact pattern text, and produces the rendering-ready union.
func buildIgnoreRulesUnion(sources []*ignoreSource) (ignoreRulesUnion, error) {
	byPattern := map[string][]ignoreRuleOccurrence{}
	contributingSet := map[*ignoreSource]bool{}
	total := 0

	for _, src := range sources {
		occs, err := parseIgnoreFile(src.path)
		if err != nil {
			return ignoreRulesUnion{}, fmt.Errorf("read %s: %w", src.path, err)
		}
		if len(occs) == 0 {
			continue
		}
		contributingSet[src] = true
		for _, occ := range occs {
			byPattern[occ.pattern] = append(byPattern[occ.pattern], ignoreRuleOccurrence{
				source: src,
				line:   occ.line,
			})
			total++
		}
	}

	patterns := make([]string, 0, len(byPattern))
	for p := range byPattern {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)

	// Tag order within each pattern row: precedence high → low, ties broken by
	// label so output stays deterministic / diffable.
	for p, occs := range byPattern {
		sorted := append([]ignoreRuleOccurrence(nil), occs...)
		sort.SliceStable(sorted, func(i, j int) bool {
			if sorted[i].source.rank != sorted[j].source.rank {
				return sorted[i].source.rank > sorted[j].source.rank
			}
			if sorted[i].source.label != sorted[j].source.label {
				return sorted[i].source.label < sorted[j].source.label
			}
			return sorted[i].line < sorted[j].line
		})
		byPattern[p] = sorted
	}

	contributing := make([]*ignoreSource, 0, len(contributingSet))
	for s := range contributingSet {
		contributing = append(contributing, s)
	}
	sort.SliceStable(contributing, func(i, j int) bool {
		if contributing[i].rank != contributing[j].rank {
			return contributing[i].rank > contributing[j].rank
		}
		return contributing[i].label < contributing[j].label
	})

	return ignoreRulesUnion{
		Patterns:     patterns,
		ByPattern:    byPattern,
		TotalRules:   total,
		Contributing: contributing,
	}, nil
}

type parsedIgnoreRule struct {
	pattern string
	line    int
}

// parseIgnoreFile yields one parsedIgnoreRule per non-comment, non-blank line.
// Negation (`!foo`) and escaped-comment (`\#foo`) lines are kept verbatim.
func parseIgnoreFile(path string) ([]parsedIgnoreRule, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rules []parsedIgnoreRule
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimRight(sc.Text(), " \t\r")
		if strings.TrimSpace(raw) == "" {
			continue
		}
		// gitignore comment: first non-whitespace is '#'. `\#` escapes a literal.
		nonWS := strings.TrimLeft(raw, " \t")
		if strings.HasPrefix(nonWS, "#") {
			continue
		}
		rules = append(rules, parsedIgnoreRule{pattern: raw, line: lineNo})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

// renderIgnoreRulesUnion prints the legend + deduped rows + summary. Inspection
// view: stdout, no clipboard. Headless / NO_COLOR strips color via the palette
// the caller chose for stdout.
func renderIgnoreRulesUnion(out io.Writer, union ignoreRulesUnion, colors platform.Palette) error {
	if len(union.Patterns) == 0 {
		_, err := fmt.Fprintln(out, "No ignore rules in effect.")
		return err
	}

	if err := writeIgnoreRulesLegend(out, union.Contributing, colors); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}

	patternWidth := 0
	for _, p := range union.Patterns {
		if len(p) > patternWidth {
			patternWidth = len(p)
		}
	}

	for _, p := range union.Patterns {
		occs := union.ByPattern[p]
		tags := make([]string, 0, len(occs))
		for _, occ := range occs {
			tags = append(tags, fmt.Sprintf("%s%s:%d%s", colors.Label, occ.source.label, occ.line, colors.Reset))
		}
		if _, err := fmt.Fprintf(out, "%-*s  [%s]\n", patternWidth, p, strings.Join(tags, ", ")); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	sourceCount := len(union.Contributing)
	if len(union.Patterns) == union.TotalRules {
		_, err := fmt.Fprintf(out, "%d %s · %d %s\n",
			len(union.Patterns), pluralizeWord("rule", len(union.Patterns)),
			sourceCount, pluralizeWord("source", sourceCount))
		return err
	}
	_, err := fmt.Fprintf(out, "%d %s (%d %s) · %d %s\n",
		len(union.Patterns), pluralizeWord("pattern", len(union.Patterns)),
		union.TotalRules, pluralizeWord("rule", union.TotalRules),
		sourceCount, pluralizeWord("source", sourceCount))
	return err
}

// writeIgnoreRulesLegend groups contributing sources by kind so e.g. several
// nested .gitignores share one row, then ends with the precedence one-liner
// users specifically asked for.
func writeIgnoreRulesLegend(out io.Writer, contributing []*ignoreSource, colors platform.Palette) error {
	type legendRow struct {
		label     string
		scopeDesc string
		paths     []string
		rank      int
	}
	groups := map[string]*legendRow{}
	var order []*legendRow
	for _, s := range contributing {
		key := legendGroupKey(s.kind)
		display := platform.DisplayPath(s.path)
		if g, ok := groups[key]; ok {
			g.paths = append(g.paths, display)
			if s.rank > g.rank {
				g.rank = s.rank
			}
			continue
		}
		row := &legendRow{
			label:     legendKindLabel(s.kind),
			scopeDesc: legendKindScope(s.kind),
			paths:     []string{display},
			rank:      s.rank,
		}
		groups[key] = row
		order = append(order, row)
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].rank > order[j].rank })

	if _, err := fmt.Fprintln(out, "Sources (precedence high → low — a higher source wins a `!` re-include conflict):"); err != nil {
		return err
	}

	// Column widths: label and scopeDesc.
	labelWidth, scopeWidth := 0, 0
	for _, r := range order {
		if len(r.label) > labelWidth {
			labelWidth = len(r.label)
		}
		if len(r.scopeDesc) > scopeWidth {
			scopeWidth = len(r.scopeDesc)
		}
	}

	for _, r := range order {
		if _, err := fmt.Fprintf(out, "  %s%-*s%s  %-*s  %s\n",
			colors.Label, labelWidth, r.label, colors.Reset,
			scopeWidth, r.scopeDesc,
			strings.Join(r.paths, ", "),
		); err != nil {
			return err
		}
	}

	if hasContributingKind(contributing, sourceKindGitignoreRoot, sourceKindGitignoreNested) &&
		hasContributingKind(contributing, sourceKindHiss) {
		if _, err := fmt.Fprintln(out, "  → .gitignore overrides .hiss"); err != nil {
			return err
		}
	}
	return nil
}

func legendGroupKey(k ignoreSourceKind) string {
	if k == sourceKindGitignoreRoot || k == sourceKindGitignoreNested {
		return "gitignore"
	}
	return fmt.Sprintf("k%d", int(k))
}

func legendKindLabel(k ignoreSourceKind) string {
	switch k {
	case sourceKindGitignoreRoot, sourceKindGitignoreNested:
		return ".gitignore"
	case sourceKindInfoExclude:
		return ".git/info/exclude"
	case sourceKindHiss:
		return ".hiss"
	case sourceKindGlobalExcludes:
		return "(global)"
	}
	return ""
}

func legendKindScope(k ignoreSourceKind) string {
	switch k {
	case sourceKindGitignoreRoot, sourceKindGitignoreNested:
		return "this repo, per-folder"
	case sourceKindInfoExclude:
		return "this repo, local only"
	case sourceKindHiss:
		return "global, every project"
	case sourceKindGlobalExcludes:
		return "your machine, all repos"
	}
	return ""
}

func hasContributingKind(sources []*ignoreSource, kinds ...ignoreSourceKind) bool {
	for _, s := range sources {
		for _, k := range kinds {
			if s.kind == k {
				return true
			}
		}
	}
	return false
}

func pluralizeWord(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
