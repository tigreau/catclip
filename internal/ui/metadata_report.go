package ui

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
	renderpkg "github.com/tigreau/catclip/internal/render"
	"github.com/tigreau/catclip/internal/search"
)

// MetadataReport is the immutable, body-free payload produced by --metadata.
// Interactive startup builds it once and carries it through the sink picker so
// measuring, previewing, and final emission all describe the same selection.
type MetadataReport struct {
	Root            string
	Generated       string
	Git             *MetadataGitSummary
	Scopes          []MetadataScope
	Composition     []MetadataAggregate
	DirectoryGroups []MetadataAggregate
	LargestFiles    []MetadataLargestFile
	Rows            []MetadataRow
	TotalBytes      int64
	TextTokens      int64
	BinaryCount     int

	encodedBytes int64
	encodedKnown bool
}

type MetadataScope struct {
	Summary      string
	NoIgnore     bool
	Selected     int
	Visible      int
	VisibleKnown bool
	Ignored      MetadataIgnoredSummary
}

type MetadataIgnoredSummary struct {
	Total int
	Rows  []MetadataIgnoredPath
}

type MetadataIgnoredPath struct {
	Path    string
	Kind    string
	Source  string
	Pattern string
}

type MetadataGitSummary struct {
	Branch         string
	Commit         string
	Modified       int
	Staged         int
	StagedModified int
	Untracked      int
}

type MetadataAggregate struct {
	Label       string
	Count       int
	Bytes       int64
	Tokens      int64
	BinaryCount int
}

type MetadataLargestFile struct {
	Path   string
	Bytes  int64
	Tokens int64
	Binary bool
}

type MetadataRow struct {
	Bytes    int64
	Path     string
	Size     string
	Tokens   string
	Git      string
	Modified string
	Flag     string
}

const (
	metadataIgnoredDisplayLimit = 20
	metadataAggregateLimit      = 5
	metadataLargeSummaryMinimum = 20
)

// BuildMetadataReport materializes metadata exclusively from the selected
// discovery generation and output plan. Binary classification, when needed,
// is restricted to those already-selected paths and cannot expand membership.
func BuildMetadataReport(workingDir string, gitCtx git.Context, scopes []discovery.Scope, plan output.Plan, report output.Report, withBinaries bool, now time.Time) (*MetadataReport, error) {
	finishBench := platform.InternalBenchSpan("ui.metadata.build", "items", platform.InternalBenchInt(plan.Len()))
	defer finishBench()
	finishStats := platform.InternalBenchSpan("ui.metadata.ensure_stats")
	entries, err := discovery.EnsureEntryModTimes(plan.EntriesInEmissionOrder(), workingDir)
	finishStats("err", platform.InternalBenchError(err), "entries", platform.InternalBenchInt(len(entries)))
	if err != nil {
		return nil, err
	}

	textPaths := map[string]struct{}{}
	if withBinaries {
		paths := make([]string, 0, len(entries))
		for _, entry := range entries {
			paths = append(paths, entry.RelPath)
		}
		textPaths, err = search.ClassifyTextPaths(workingDir, paths)
		if err != nil {
			return nil, err
		}
	}

	result := &MetadataReport{
		Root:      metadataRootName(workingDir),
		Generated: now.Format(time.RFC3339),
		Git:       buildMetadataGitSummary(workingDir, gitCtx, entries, report.Statuses),
	}
	result.Scopes = make([]MetadataScope, 0, len(scopes))
	for _, discoveredScope := range scopes {
		result.Scopes = append(result.Scopes, MetadataScope{
			Summary:  metadataScopeSummary(discoveredScope.Scope),
			NoIgnore: metadataScopeNoIgnore(discoveredScope),
			Selected: len(discoveredScope.Entries),
		})
	}
	finishIgnore := platform.InternalBenchSpan("ui.metadata.ignore_trace", "scopes", platform.InternalBenchInt(len(scopes)))
	err = populateMetadataIgnoreTrace(workingDir, scopes, result.Scopes)
	finishIgnore("err", platform.InternalBenchError(err))
	if err != nil {
		return nil, err
	}

	finishRows := platform.InternalBenchSpan("ui.metadata.rows", "entries", platform.InternalBenchInt(len(entries)))
	result.Rows = make([]MetadataRow, 0, len(entries))
	composition := make(map[string]*MetadataAggregate)
	directories := make(map[string]*MetadataAggregate)
	largest := make([]MetadataLargestFile, 0, metadataAggregateLimit)
	for _, entry := range entries {
		size := entry.SizeBytes
		if !entry.SizeKnown {
			size = report.Sizes[entry.RelPath]
		}
		result.TotalBytes += size
		humanSize, tokens := renderpkg.FormatSizeAndTokens(size, 1)
		row := MetadataRow{
			Bytes:    size,
			Path:     entry.RelPath,
			Size:     humanSize,
			Tokens:   fmt.Sprintf("~%s", formatMetadataInteger(tokens)),
			Git:      metadataGitStatus(report.Statuses[entry.RelPath]),
			Modified: formatFinderModifiedSpec(now, entry.ModTime),
		}
		binary := false
		if withBinaries {
			if _, text := textPaths[entry.RelPath]; !text {
				row.Tokens = "—"
				row.Flag = "binary"
				binary = true
				result.BinaryCount++
			} else {
				result.TextTokens += tokens
			}
		} else {
			result.TextTokens += tokens
		}
		result.Rows = append(result.Rows, row)

		extension := strings.ToLower(path.Ext(metadataSlashPath(entry.RelPath)))
		if extension == "" {
			extension = "[no extension]"
		}
		addMetadataAggregate(composition, extension, size, tokens, binary)
		addMetadataAggregate(directories, metadataTopDirectory(entry.RelPath), size, tokens, binary)
		largest = retainMetadataLargest(largest, MetadataLargestFile{Path: entry.RelPath, Bytes: size, Tokens: tokens, Binary: binary}, metadataAggregateLimit)
	}
	result.Composition = sortedMetadataAggregates(composition, metadataAggregateLimit)
	if len(entries) >= metadataLargeSummaryMinimum {
		result.DirectoryGroups = sortedMetadataAggregates(directories, metadataAggregateLimit)
		if len(directories) <= 1 {
			result.DirectoryGroups = nil
		}
		result.LargestFiles = largest
	}
	finishRows("binaries", platform.InternalBenchInt(result.BinaryCount))
	return result, nil
}

func metadataRootName(workingDir string) string {
	clean := filepath.Clean(workingDir)
	name := filepath.Base(clean)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return filepath.ToSlash(clean)
	}
	return name
}

func metadataScopeSummary(scope command.ExecutionScope) string {
	filtered := scope
	filtered.NoIgnore = false
	filtered.Stages = make([]command.Stage, 0, len(scope.Stages))
	for _, stage := range scope.Stages {
		if stage.Kind != command.StageNoIgnore {
			filtered.Stages = append(filtered.Stages, stage)
		}
	}
	args := command.CanonicalScopeArgs(filtered)
	if len(args) == 0 {
		return "."
	}
	return strings.Join(args, " ")
}

func metadataGitStatus(status string) string {
	if status == "" {
		return "-"
	}
	return status
}

func metadataScopeNoIgnore(scope discovery.Scope) bool {
	return scope.Scope.NoIgnore || scope.Scope.HasStage(command.StageNoIgnore)
}

// populateMetadataIgnoreTrace performs one metadata-only repetition of the
// normal-ignore rg walk. Its stdout gives exact raw visible coverage and its
// debug stream gives causal ignored boundary paths. Neither stream can change
// the already-selected entries carried by the discovery generation.
func populateMetadataIgnoreTrace(workingDir string, scopes []discovery.Scope, summaries []MetadataScope) error {
	active := make([]int, 0, len(scopes))
	matchers := make([]metadataScopeMatcher, len(scopes))
	wide := false
	rootSet := make(map[string]struct{})
	for i, scope := range scopes {
		if metadataScopeNoIgnore(scope) {
			continue
		}
		roots, traceable, scopeWide := metadataTraceScopeRoots(workingDir, scope)
		if !traceable {
			continue
		}
		active = append(active, i)
		matchers[i] = newMetadataScopeMatcher(workingDir, scope)
		if scopeWide {
			wide = true
			continue
		}
		for _, root := range roots {
			rootSet[metadataSlashPath(root)] = struct{}{}
		}
	}
	if len(active) == 0 {
		return nil
	}

	var roots []string
	if !wide {
		for root := range rootSet {
			roots = append(roots, root)
		}
		roots = compactMetadataRoots(workingDir, roots)
	}
	hissPath := discovery.GlobalHissPath()
	if !metadataRegularFile(hissPath) {
		hissPath = ""
	}
	collectors := make([]*metadataIgnoredCollector, len(scopes))
	for _, i := range active {
		collectors[i] = &metadataIgnoredCollector{
			workingDir:     workingDir,
			globalHissPath: hissPath,
		}
		summaries[i].VisibleKnown = true
	}
	_, err := search.RunRipgrepIgnoreTrace(workingDir, search.RipgrepFileOptions{
		Paths:    roots,
		HissPath: hissPath,
		Enumeration: search.MembershipEnumerationContext{
			Reason:    search.MembershipReasonMetadataIgnoreTrace,
			Authority: search.MembershipAuthorityDiagnostic,
		},
	}, func(rel string) {
		for _, i := range active {
			if matchers[i].matches(rel) {
				summaries[i].Visible++
			}
		}
	}, func(record search.IgnoreTraceRecord) {
		for _, i := range active {
			if matchers[i].matches(record.Path) {
				collectors[i].add(record)
			}
		}
	})
	if err != nil {
		return fmt.Errorf("trace metadata ignore decisions: %w", err)
	}
	for _, i := range active {
		summaries[i].Ignored = collectors[i].summary()
	}
	return nil
}

// metadataTraceScopeRoots returns the already-resolved literal roots that may
// safely bound the diagnostic walk. A genuinely wide target or glob stays
// wide; a missing/unresolved target is not permission to scan the repository.
func metadataTraceScopeRoots(workingDir string, scope discovery.Scope) (roots []string, traceable, wide bool) {
	if len(scope.Scope.Targets) == 0 {
		return nil, true, true
	}
	seen := make(map[string]struct{})
	add := func(value string) {
		rel := metadataSlashPath(value)
		if rel == "" || rel == "." {
			return
		}
		seen[rel] = struct{}{}
	}
	for _, target := range scope.Scope.Targets {
		if strings.ContainsAny(target, "*?[") {
			return nil, true, true
		}
		rel := metadataSlashPath(target)
		if rel == "" || rel == "." {
			return nil, true, true
		}
	}
	for _, target := range scope.ResolvedTargets {
		add(target.Path)
	}
	// Compatibility for manually-built/legacy scopes that predate explicit
	// resolution evidence. Production discovery records every resolved literal,
	// so it reaches no filesystem lookup here.
	if len(scope.ResolvedTargets) == 0 {
		for _, target := range scope.Scope.Targets {
			rel := metadataSlashPath(target)
			if _, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(rel))); err == nil {
				add(rel)
			}
		}
	}
	if len(seen) == 0 {
		return nil, false, false
	}
	roots = make([]string, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	return roots, true, false
}

type metadataScopeMatcher struct {
	wide  bool
	files map[string]struct{}
	dirs  []string
	globs []string
}

func newMetadataScopeMatcher(workingDir string, scope discovery.Scope) metadataScopeMatcher {
	matcher := metadataScopeMatcher{files: make(map[string]struct{})}
	if len(scope.Scope.Targets) == 0 {
		matcher.wide = true
	}
	addLiteral := func(value string) {
		rel := metadataSlashPath(value)
		if rel == "" || rel == "." {
			matcher.wide = true
			return
		}
		info, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(rel)))
		if err == nil && info.IsDir() {
			matcher.dirs = append(matcher.dirs, rel)
			return
		}
		matcher.files[rel] = struct{}{}
	}
	for _, target := range scope.Scope.Targets {
		if strings.ContainsAny(target, "*?[") {
			matcher.globs = append(matcher.globs, metadataSlashPath(target))
			continue
		}
		if len(scope.ResolvedTargets) == 0 {
			addLiteral(target)
		}
	}
	for _, target := range scope.ResolvedTargets {
		rel := metadataSlashPath(target.Path)
		switch target.Kind {
		case discovery.ResolvedTargetDir:
			matcher.dirs = append(matcher.dirs, rel)
		case discovery.ResolvedTargetFile:
			matcher.files[rel] = struct{}{}
		}
	}
	// Older/manually-constructed scopes may not carry explicit resolution
	// evidence. Retain the entry-root fallback for those callers only; mixing it
	// with ResolvedTargets would broaden a resolved file to its parent directory.
	if len(scope.ResolvedTargets) == 0 {
		for _, entry := range scope.Entries {
			if entry.TargetRoot != "" && entry.TargetRoot != "." {
				addLiteral(entry.TargetRoot)
			}
		}
	}
	sort.Strings(matcher.dirs)
	matcher.dirs = compactMetadataStringRoots(matcher.dirs)
	return matcher
}

func (matcher metadataScopeMatcher) matches(value string) bool {
	rel := metadataSlashPath(value)
	if matcher.wide {
		return true
	}
	if _, ok := matcher.files[rel]; ok {
		return true
	}
	for _, dir := range matcher.dirs {
		if rel == dir || strings.HasPrefix(rel, strings.TrimSuffix(dir, "/")+"/") {
			return true
		}
	}
	for _, pattern := range matcher.globs {
		if matched, _ := path.Match(pattern, rel); matched {
			return true
		}
		if matched, _ := path.Match(pattern, path.Base(rel)); matched {
			return true
		}
	}
	return false
}

func compactMetadataRoots(workingDir string, roots []string) []string {
	sort.Slice(roots, func(i, j int) bool {
		leftDepth, rightDepth := strings.Count(roots[i], "/"), strings.Count(roots[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return roots[i] < roots[j]
	})
	kept := make([]string, 0, len(roots))
	for _, root := range roots {
		covered := false
		for _, parent := range kept {
			info, err := os.Stat(filepath.Join(workingDir, filepath.FromSlash(parent)))
			if err == nil && info.IsDir() && strings.HasPrefix(root, strings.TrimSuffix(parent, "/")+"/") {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, root)
		}
	}
	return kept
}

func compactMetadataStringRoots(roots []string) []string {
	kept := roots[:0]
	for _, root := range roots {
		covered := false
		for _, parent := range kept {
			if root == parent || strings.HasPrefix(root, strings.TrimSuffix(parent, "/")+"/") {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, root)
		}
	}
	return kept
}

func metadataSlashPath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "./")
	if value == "/" {
		return "."
	}
	return value
}

type metadataIgnoredCollector struct {
	workingDir     string
	globalHissPath string
	total          int
	rows           []metadataIgnoredCandidate
}

type metadataIgnoredCandidate struct {
	search.IgnoreTraceRecord
	depth int
}

func metadataIgnoredCandidateLess(a, b metadataIgnoredCandidate) bool {
	if a.RuleDirectoryOnly != b.RuleDirectoryOnly {
		return a.RuleDirectoryOnly
	}
	if a.depth != b.depth {
		return a.depth < b.depth
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	return a.Source < b.Source
}

func (collector *metadataIgnoredCollector) add(record search.IgnoreTraceRecord) {
	collector.total++
	// Directory-only rules already provide useful type evidence. Retain a
	// bounded priority list without statting every rejected path or repeatedly
	// cleaning/sorting the same twenty paths for every incoming record.
	record.Path = metadataSlashPath(record.Path)
	candidate := metadataIgnoredCandidate{IgnoreTraceRecord: record, depth: strings.Count(record.Path, "/")}
	index := sort.Search(len(collector.rows), func(i int) bool { return metadataIgnoredCandidateLess(candidate, collector.rows[i]) })
	if index >= metadataIgnoredDisplayLimit {
		return
	}
	if len(collector.rows) < metadataIgnoredDisplayLimit {
		collector.rows = append(collector.rows, metadataIgnoredCandidate{})
	}
	copy(collector.rows[index+1:], collector.rows[index:len(collector.rows)-1])
	collector.rows[index] = candidate
}

func (collector *metadataIgnoredCollector) summary() MetadataIgnoredSummary {
	summary := MetadataIgnoredSummary{Total: collector.total, Rows: make([]MetadataIgnoredPath, 0, len(collector.rows))}
	for _, record := range collector.rows {
		kind := "path"
		if info, err := os.Stat(filepath.Join(collector.workingDir, filepath.FromSlash(record.Path))); err == nil {
			if info.IsDir() {
				kind = "directory"
			} else if info.Mode().IsRegular() {
				kind = "file"
			}
		} else if record.RuleDirectoryOnly {
			kind = "directory"
		}
		source := strings.TrimSpace(record.Source)
		if source == "" {
			source = "(ripgrep)"
		} else {
			if !filepath.IsAbs(source) {
				source = filepath.Join(collector.workingDir, filepath.FromSlash(source))
			}
			if collector.globalHissPath != "" && metadataPathsReferToSameFile(source, collector.globalHissPath) {
				// The global Catclip ignore file is user configuration, even when
				// its configured location happens to sit below the current working
				// directory. Never make it look like a project-owned ignore file.
				source = metadataDisplayGlobalHissPath(collector.globalHissPath)
			} else {
				source = metadataDisplayPath(collector.workingDir, source)
			}
		}
		summary.Rows = append(summary.Rows, MetadataIgnoredPath{
			Path:    metadataSlashPath(record.Path),
			Kind:    kind,
			Source:  source,
			Pattern: metadataInlineValue(record.Pattern),
		})
	}
	// Only the retained rows incur the existing bounded metadata checks.
	// A rule without a trailing slash may also name a directory; promote it
	// after those checks, but never probe the entire ignored inventory.
	sort.Slice(summary.Rows, func(i, j int) bool {
		a, b := summary.Rows[i], summary.Rows[j]
		if (a.Kind == "directory") != (b.Kind == "directory") {
			return a.Kind == "directory"
		}
		if da, db := strings.Count(a.Path, "/"), strings.Count(b.Path, "/"); da != db {
			return da < db
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Source < b.Source
	})
	kept := summary.Rows[:0]
	for _, row := range summary.Rows {
		covered := false
		for _, parent := range kept {
			if parent.Kind == "directory" && strings.HasPrefix(row.Path, parent.Path+"/") {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, row)
		}
	}
	summary.Rows = kept
	return summary
}

func metadataInlineValue(value string) string {
	quoted := strconv.Quote(value)
	return strings.TrimSuffix(strings.TrimPrefix(quoted, `"`), `"`)
}

func metadataRegularFile(value string) bool {
	info, err := os.Stat(value)
	return err == nil && info.Mode().IsRegular()
}

func metadataDisplayPath(workingDir, value string) string {
	canonicalWorkingDir := metadataCanonicalFilesystemPath(workingDir)
	canonicalValue := metadataCanonicalFilesystemPath(value)
	if rel, err := filepath.Rel(canonicalWorkingDir, canonicalValue); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	if display := platform.DisplayPath(value); display != value {
		return display
	}
	return platform.DisplayPath(canonicalValue)
}

func metadataDisplayGlobalHissPath(value string) string {
	clean := filepath.Clean(value)
	if display := platform.DisplayPath(clean); display != clean {
		return display
	}
	return platform.DisplayPath(metadataCanonicalFilesystemPath(clean))
}

func metadataPathsReferToSameFile(left, right string) bool {
	canonicalLeft := metadataCanonicalFilesystemPath(left)
	canonicalRight := metadataCanonicalFilesystemPath(right)
	if canonicalLeft == canonicalRight || (runtime.GOOS == "windows" && strings.EqualFold(canonicalLeft, canonicalRight)) {
		return true
	}
	leftInfo, leftErr := os.Stat(canonicalLeft)
	rightInfo, rightErr := os.Stat(canonicalRight)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func metadataCanonicalFilesystemPath(value string) string {
	clean := filepath.Clean(value)
	if runtime.GOOS == "windows" {
		clean = metadataStripWindowsExtendedPath(clean)
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = filepath.Clean(resolved)
		if runtime.GOOS == "windows" {
			clean = metadataStripWindowsExtendedPath(clean)
		}
	}
	return clean
}

func metadataStripWindowsExtendedPath(value string) string {
	if strings.HasPrefix(value, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(value, `\\?\UNC\`)
	}
	return strings.TrimPrefix(value, `\\?\`)
}

func buildMetadataGitSummary(workingDir string, gitCtx git.Context, entries []discovery.Entry, statuses map[string]string) *MetadataGitSummary {
	if !gitCtx.Enabled {
		return nil
	}
	summary := &MetadataGitSummary{}
	if branch, err := git.Capture(workingDir, "symbolic-ref", "--short", "-q", "HEAD"); err == nil {
		summary.Branch = strings.TrimSpace(branch)
	}
	if summary.Branch == "" {
		summary.Branch = "detached"
	}
	if gitCtx.HasHead {
		if commit, err := git.Capture(workingDir, "rev-parse", "--short=12", "HEAD"); err == nil {
			summary.Commit = strings.TrimSpace(commit)
		}
	}
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, duplicate := seen[entry.RelPath]; duplicate {
			continue
		}
		seen[entry.RelPath] = struct{}{}
		status := statuses[entry.RelPath]
		switch status {
		case "M":
			summary.Modified++
		case "S":
			summary.Staged++
		case "SM":
			summary.StagedModified++
		case "?":
			summary.Untracked++
		}
	}
	return summary
}

func addMetadataAggregate(groups map[string]*MetadataAggregate, label string, bytes, tokens int64, binary bool) {
	group := groups[label]
	if group == nil {
		group = &MetadataAggregate{Label: label}
		groups[label] = group
	}
	group.Count++
	group.Bytes += bytes
	if binary {
		group.BinaryCount++
	} else {
		group.Tokens += tokens
	}
}

func sortedMetadataAggregates(groups map[string]*MetadataAggregate, limit int) []MetadataAggregate {
	result := make([]MetadataAggregate, 0, len(groups))
	for _, group := range groups {
		result = append(result, *group)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Bytes != result[j].Bytes {
			return result[i].Bytes > result[j].Bytes
		}
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Label < result[j].Label
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func metadataTopDirectory(value string) string {
	rel := metadataSlashPath(value)
	if before, _, ok := strings.Cut(rel, "/"); ok {
		return before + "/"
	}
	return "[root]"
}

func retainMetadataLargest(rows []MetadataLargestFile, row MetadataLargestFile, limit int) []MetadataLargestFile {
	rows = append(rows, row)
	for i := len(rows) - 1; i > 0 && metadataLargestBefore(rows[i], rows[i-1]); i-- {
		rows[i], rows[i-1] = rows[i-1], rows[i]
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func metadataLargestBefore(left, right MetadataLargestFile) bool {
	if left.Bytes != right.Bytes {
		return left.Bytes > right.Bytes
	}
	return left.Path < right.Path
}

// WriteMetadataReport emits the canonical plain-text metadata payload. It is
// intentionally color-free for clipboard, pipes, headless, bundles, accounting,
// and byte accounting. Terminal and fzf preview styling are separate callers.
func WriteMetadataReport(w io.Writer, report *MetadataReport) error {
	return writeMetadataReport(w, report, nil)
}

func writeMetadataReport(w io.Writer, report *MetadataReport, style *metadataStyle) error {
	if report == nil {
		return fmt.Errorf("internal error: metadata report is unavailable")
	}
	root := report.Root
	if style != nil {
		root = style.text(root, style.colors.Prompt)
	}
	if _, err := fmt.Fprintf(w, "Root: %s\n", root); err != nil {
		return err
	}
	if report.Generated != "" {
		generated := report.Generated
		if style != nil {
			generated = style.text(generated, style.colors.Dim)
		}
		if _, err := fmt.Fprintf(w, "Generated: %s\n", generated); err != nil {
			return err
		}
	}
	if report.Git != nil {
		if err := writeMetadataGitSummary(w, report.Git, style); err != nil {
			return err
		}
	}
	if len(report.Scopes) == 1 {
		summary := report.Scopes[0].Summary
		if style != nil {
			summary = style.text(summary, style.colors.Prompt)
		}
		if _, err := fmt.Fprintf(w, "Scope: %s\n", summary); err != nil {
			return err
		}
		if err := writeMetadataScopeFacts(w, report.Scopes[0], "", style); err != nil {
			return err
		}
	} else if len(report.Scopes) > 1 {
		heading := "Scopes:\n"
		if style != nil {
			heading = style.text(heading, style.colors.Bold)
		}
		if _, err := io.WriteString(w, heading); err != nil {
			return err
		}
		for i, scope := range report.Scopes {
			summary := scope.Summary
			if style != nil {
				summary = style.text(summary, style.colors.Prompt)
			}
			if _, err := fmt.Fprintf(w, "  %d. %s\n", i+1, summary); err != nil {
				return err
			}
			if err := writeMetadataScopeFacts(w, scope, "     ", style); err != nil {
				return err
			}
		}
	}
	if err := writeMetadataAggregates(w, "Composition (largest 5 by size)", report.Composition, style); err != nil {
		return err
	}
	if err := writeMetadataAggregates(w, "Directory groups (largest 5 by size)", report.DirectoryGroups, style); err != nil {
		return err
	}
	if err := writeMetadataLargestFiles(w, report.LargestFiles, style); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if err := writeMetadataRows(w, report.Rows, style); err != nil {
		return err
	}
	return writeMetadataFooter(w, report, style)
}

func writeMetadataGitSummary(w io.Writer, summary *MetadataGitSummary, styles ...*metadataStyle) error {
	style := metadataStyleFrom(styles)
	identity := summary.Branch
	if summary.Commit != "" {
		identity += " @ " + summary.Commit
	} else if summary.Branch != "detached" {
		identity += " (unborn)"
	}
	changes := make([]string, 0, 4)
	for _, item := range []struct {
		count int
		label string
	}{
		{summary.Modified, "modified"},
		{summary.Staged, "staged"},
		{summary.StagedModified, "staged+modified"},
		{summary.Untracked, "untracked"},
	} {
		if item.count > 0 {
			changes = append(changes, fmt.Sprintf("%d %s", item.count, item.label))
		}
	}
	if len(changes) == 0 {
		if style != nil {
			return style.line(w, style.colors.Git, "Git: %s · clean selection\n", identity)
		}
		_, err := fmt.Fprintf(w, "Git: %s · clean selection\n", identity)
		return err
	}
	if style != nil {
		return style.line(w, style.colors.Git, "Git: %s · %s\n", identity, strings.Join(changes, " · "))
	}
	_, err := fmt.Fprintf(w, "Git: %s · %s\n", identity, strings.Join(changes, " · "))
	return err
}

func writeMetadataScopeFacts(w io.Writer, scope MetadataScope, indent string, styles ...*metadataStyle) error {
	style := metadataStyleFrom(styles)
	if scope.NoIgnore {
		_, err := fmt.Fprintf(w, "%sSelected: %d %s\n", indent, scope.Selected, metadataFileWord(scope.Selected))
		return err
	}
	if scope.VisibleKnown {
		if _, err := fmt.Fprintf(w, "%sCoverage: %d raw visible %s · %d selected %s\n",
			indent, scope.Visible, metadataFileWord(scope.Visible), scope.Selected, metadataFileWord(scope.Selected)); err != nil {
			return err
		}
	} else {
		return nil
	}
	if scope.Ignored.Total == 0 {
		_, err := fmt.Fprintf(w, "%sIgnored within target scope: none\n", indent)
		return err
	}
	boundaryWord := "paths"
	if scope.Ignored.Total == 1 {
		boundaryWord = "path"
	}
	if _, err := fmt.Fprintf(w, "%sIgnored within target scope: %d boundary %s\n", indent, scope.Ignored.Total, boundaryWord); err != nil {
		return err
	}
	for _, row := range scope.Ignored.Rows {
		if style != nil {
			if err := style.line(w, style.colors.Dim, "%s  %s [%s] · source: %s · pattern: %s\n", indent, row.Path, row.Kind, row.Source, row.Pattern); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s  %s [%s] · source: %s · pattern: %s\n", indent, row.Path, row.Kind, row.Source, row.Pattern); err != nil {
			return err
		}
	}
	if omitted := scope.Ignored.Total - len(scope.Ignored.Rows); omitted > 0 {
		_, err := fmt.Fprintf(w, "%s  … %d more ignored boundary paths\n", indent, omitted)
		return err
	}
	return nil
}

func metadataFileWord(count int) string {
	if count == 1 {
		return "file"
	}
	return "files"
}

func writeMetadataAggregates(w io.Writer, heading string, groups []MetadataAggregate, styles ...*metadataStyle) error {
	if len(groups) == 0 {
		return nil
	}
	if style := metadataStyleFrom(styles); style != nil {
		heading = style.text(heading, style.colors.Bold+style.colors.Prompt)
	}
	if _, err := fmt.Fprintf(w, "\n%s:\n", heading); err != nil {
		return err
	}
	for _, group := range groups {
		humanSize, _ := renderpkg.FormatSizeAndTokens(group.Bytes, group.Count)
		fileWord := "files"
		if group.Count == 1 {
			fileWord = "file"
		}
		tokenLabel := fmt.Sprintf("~%s tokens", formatMetadataInteger(group.Tokens))
		if group.BinaryCount > 0 {
			binaryWord := "binaries"
			if group.BinaryCount == 1 {
				binaryWord = "binary"
			}
			tokenLabel = fmt.Sprintf("~%s text tokens · %d %s", formatMetadataInteger(group.Tokens), group.BinaryCount, binaryWord)
		}
		sizeLabel := fmt.Sprintf("%9s", humanSize)
		if style := metadataStyleFrom(styles); style != nil {
			sizeLabel = style.size(sizeLabel, group.Bytes)
		}
		if _, err := fmt.Fprintf(w, "  %-16s %5d %s · %s · %s\n", group.Label, group.Count, fileWord, sizeLabel, tokenLabel); err != nil {
			return err
		}
	}
	return nil
}

func writeMetadataLargestFiles(w io.Writer, rows []MetadataLargestFile, styles ...*metadataStyle) error {
	style := metadataStyleFrom(styles)
	if len(rows) == 0 {
		return nil
	}
	heading := "\nLargest selected files:\n"
	if style != nil {
		heading = style.text(heading, style.colors.Bold+style.colors.Prompt)
	}
	if _, err := io.WriteString(w, heading); err != nil {
		return err
	}
	for _, row := range rows {
		humanSize, _ := renderpkg.FormatSizeAndTokens(row.Bytes, 1)
		tokenLabel := fmt.Sprintf("~%s tokens", formatMetadataInteger(row.Tokens))
		if row.Binary {
			tokenLabel = "binary"
		}
		path := row.Path
		if style != nil {
			path = style.path(path)
			humanSize = style.size(humanSize, row.Bytes)
		}
		if _, err := fmt.Fprintf(w, "  %s · %s · %s\n", path, humanSize, tokenLabel); err != nil {
			return err
		}
	}
	return nil
}

func writeMetadataRows(w io.Writer, rows []MetadataRow, styles ...*metadataStyle) error {
	style := metadataStyleFrom(styles)
	widths := []int{len("PATH"), len("SIZE"), len("TOKENS"), len("GIT"), len("MODIFIED")}
	withFlags := false
	for _, row := range rows {
		values := []string{row.Path, row.Size, row.Tokens, row.Git, row.Modified}
		for i, value := range values {
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
		withFlags = withFlags || row.Flag != ""
	}
	if style != nil {
		heading := fmt.Sprintf("%-*s  %*s  %*s  %-*s  %s\n", widths[0], "PATH", widths[1], "SIZE", widths[2], "TOKENS", widths[3], "GIT", "MODIFIED")
		if withFlags {
			heading = fmt.Sprintf("%-*s  %*s  %*s  %-*s  %-*s  FLAGS\n", widths[0], "PATH", widths[1], "SIZE", widths[2], "TOKENS", widths[3], "GIT", widths[4], "MODIFIED")
		}
		if _, err := io.WriteString(w, style.text(heading, style.colors.Bold)); err != nil {
			return err
		}
	} else if withFlags {
		if _, err := fmt.Fprintf(w, "%-*s  %*s  %*s  %-*s  %-*s  FLAGS\n", widths[0], "PATH", widths[1], "SIZE", widths[2], "TOKENS", widths[3], "GIT", widths[4], "MODIFIED"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(w, "%-*s  %*s  %*s  %-*s  %s\n", widths[0], "PATH", widths[1], "SIZE", widths[2], "TOKENS", widths[3], "GIT", "MODIFIED"); err != nil {
		return err
	}
	for _, row := range rows {
		if style != nil {
			paddedPath := fmt.Sprintf("%-*s", widths[0], row.Path)
			path := style.path(row.Path) + paddedPath[len(row.Path):]
			size := style.size(fmt.Sprintf("%*s", widths[1], row.Size), row.Bytes)
			git := style.text(fmt.Sprintf("%-*s", widths[3], row.Git), style.gitColor(row.Git))
			modified := row.Modified
			if withFlags {
				modified = fmt.Sprintf("%-*s", widths[4], modified)
			}
			modified = style.modified(modified)
			if withFlags {
				if _, err := fmt.Fprintf(w, "%s  %s  %*s  %s  %s  %s\n", path, size, widths[2], row.Tokens, git, modified, style.text(row.Flag, style.colors.Dim)); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(w, "%s  %s  %*s  %s  %s\n", path, size, widths[2], row.Tokens, git, modified); err != nil {
				return err
			}
			continue
		}
		if withFlags {
			if _, err := fmt.Fprintf(w, "%-*s  %*s  %*s  %-*s  %-*s  %s\n", widths[0], row.Path, widths[1], row.Size, widths[2], row.Tokens, widths[3], row.Git, widths[4], row.Modified, row.Flag); err != nil {
				return err
			}
		} else if _, err := fmt.Fprintf(w, "%-*s  %*s  %*s  %-*s  %s\n", widths[0], row.Path, widths[1], row.Size, widths[2], row.Tokens, widths[3], row.Git, row.Modified); err != nil {
			return err
		}
	}
	return nil
}

func writeMetadataFooter(w io.Writer, report *MetadataReport, styles ...*metadataStyle) error {
	humanSize, _ := renderpkg.FormatSizeAndTokens(report.TotalBytes, len(report.Rows))
	word := "files"
	if len(report.Rows) == 1 {
		word = "file"
	}
	tokenLabel := "tokens"
	if report.BinaryCount > 0 {
		tokenLabel = "text tokens"
	}
	countLabel := fmt.Sprintf("%d %s", len(report.Rows), word)
	if style := metadataStyleFrom(styles); style != nil {
		countLabel = style.text(countLabel, style.colors.Bold)
		humanSize = style.size(humanSize, report.TotalBytes)
	}
	footer := fmt.Sprintf("\n%s · %s · ~%s %s", countLabel, humanSize, formatMetadataInteger(report.TextTokens), tokenLabel)
	if report.BinaryCount > 0 {
		binaryWord := "binaries"
		if report.BinaryCount == 1 {
			binaryWord = "binary"
		}
		footer += fmt.Sprintf(" · %d %s", report.BinaryCount, binaryWord)
	}
	_, err := io.WriteString(w, footer+"\n")
	return err
}

func formatMetadataInteger(value int64) string {
	digits := fmt.Sprintf("%d", value)
	for i := len(digits) - 3; i > 0; i -= 3 {
		digits = digits[:i] + "," + digits[i:]
	}
	return digits
}

type metadataCountingWriter struct{ n int64 }

func (w *metadataCountingWriter) Write(p []byte) (int, error) {
	w.n += int64(len(p))
	return len(p), nil
}

func (report *MetadataReport) EncodedSize() (int64, error) {
	if report == nil {
		return 0, fmt.Errorf("internal error: metadata report is unavailable")
	}
	if report.encodedKnown {
		return report.encodedBytes, nil
	}
	finishBench := platform.InternalBenchSpan("ui.metadata.measure", "rows", platform.InternalBenchInt(len(report.Rows)))
	w := &metadataCountingWriter{}
	if err := WriteMetadataReport(w, report); err != nil {
		finishBench("err", "true")
		return 0, err
	}
	report.encodedBytes = w.n
	report.encodedKnown = true
	finishBench("err", "false", "bytes", fmt.Sprintf("%d", w.n))
	return w.n, nil
}
