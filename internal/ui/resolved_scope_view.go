package ui

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/platform"
	"github.com/tigreau/catclip/internal/search"
)

type resolvedScopeView struct {
	Invocation command.Invocation
	Render     RenderConfig
	Progress   interactiveProgressExtras
	GitContext git.Context
	Scopes     []command.ExecutionScope
	ScopeIndex int
	Scope      command.ExecutionScope
	Entries    []discovery.Entry
	Discovered discovery.Scope
	Duration   time.Duration
	inventory  *scopeViewInventory
	fileIDs    []uint32
	// snippetMatchLines is a state-local overlay keyed by stable inventory ID.
	// Match offsets belong to one --snippet pattern and therefore cannot live
	// on the shared path inventory used by sibling history states.
	snippetMatchLines map[uint32][]int
}

type scopeViewMemoEntry struct {
	inventory         *scopeViewInventory
	fileIDs           []uint32
	args              []string
	view              resolvedScopeView
	stateID           uint64
	parentID          uint64
	projectMetadata   bool
	snippetMatchLines map[uint32][]int
	// targetPreviewInventoryPath belongs only to a freshly adopted target
	// selection. Derived states intentionally do not inherit it because their
	// membership or output projection no longer matches the compact artifact.
	targetPreviewInventoryPath string
	targetSelectionBase        bool
	targetPreviewInventoryDir  string
	targetPreviewInventoryBusy bool

	checkpointPath string
	checkpointDir  string
	checkpointBusy bool
}

type scopeViewInventory struct {
	mu                  sync.RWMutex
	metadataMu          sync.Mutex
	entries             []discovery.Entry
	initialSizeKnown    []bool
	initialModTimeKnown []bool
	metadata            []search.FileMetadata
	metadataKnown       []bool
	metadataSealed      bool

	observationMu      sync.Mutex
	scopedIgnoredKnown bool
	hasScopedIgnored   bool
	gitStatusKnown     bool
	gitStatus          map[string]string
}

type retainedContentStageResult struct {
	kind         command.StageKind
	pattern      string
	survivors    map[string]struct{}
	snippetLines map[string][]int
}

// scopeViewMemo retains every derived state in the current interactive
// invocation. Exact argv revisits (including undo) reuse the retained state;
// a child that appends one path-only stage may be derived from its parent.
// The command parser remains the authority: states that do not prove this
// narrow relationship fall back to canonical discovery.
var (
	scopeViewMemoMu               sync.Mutex
	scopeViewMemoCond             = sync.NewCond(&scopeViewMemoMu)
	scopeViewMemoValues           = make(map[string]scopeViewMemoEntry)
	scopeViewMemoNextID           uint64
	scopeViewMemoTargetMetadata   map[string]search.FileMetadata
	scopeViewMemoContentStages    = make(map[string]retainedContentStageResult)
	scopeViewMemoGenerationSealed bool
)

func materializeScopeView(entry scopeViewMemoEntry) resolvedScopeView {
	return materializeScopeViewWithMetadata(entry, false)
}

func materializeScopeViewWithMetadata(entry scopeViewMemoEntry, includeCaptured bool) resolvedScopeView {
	includeCaptured = includeCaptured || entry.projectMetadata
	view := entry.view
	view.Render.Scopes = cloneExecutionScopes(view.Render.Scopes)
	view.Scopes = cloneExecutionScopes(view.Scopes)
	view.Scope = cloneExecutionScope(view.Scope)
	view.Discovered.Scope = cloneExecutionScope(view.Discovered.Scope)
	view.Discovered.Entries = cloneDiscoveryEntries(view.Discovered.Entries)
	view.Discovered.Diagnostics = append([]discovery.Diagnostic(nil), view.Discovered.Diagnostics...)
	view.Discovered.Notices = cloneStringSlice(view.Discovered.Notices)
	view.inventory = entry.inventory
	view.fileIDs = append([]uint32(nil), entry.fileIDs...)
	view.snippetMatchLines = cloneSnippetLineOverlay(entry.snippetMatchLines)
	view.Entries = make([]discovery.Entry, 0, len(entry.fileIDs))
	if entry.inventory == nil {
		return view
	}
	entry.inventory.mu.RLock()
	defer entry.inventory.mu.RUnlock()
	for _, id := range entry.fileIDs {
		if uint64(id) < uint64(len(entry.inventory.entries)) {
			projected := entry.inventory.entries[id]
			projected.SnippetMatchLines = append([]int(nil), projected.SnippetMatchLines...)
			if lines, ok := entry.snippetMatchLines[id]; ok {
				projected.SnippetMatchLines = append([]int(nil), lines...)
			}
			if !includeCaptured && uint64(id) < uint64(len(entry.inventory.initialSizeKnown)) && !entry.inventory.initialSizeKnown[id] {
				projected.SizeBytes = 0
				projected.SizeKnown = false
			}
			if !includeCaptured && uint64(id) < uint64(len(entry.inventory.initialModTimeKnown)) && !entry.inventory.initialModTimeKnown[id] {
				projected.ModTime = time.Time{}
			}
			view.Entries = append(view.Entries, projected)
		}
	}
	// The shared inventory owns file identity and reusable observations, not
	// the output projection of every child state. A derived output stage such
	// as --lines preserves the same file IDs, so re-materializing the raw
	// inventory would otherwise hand full-file entries to the sink planner.
	// Canonical EvaluateScope performs this stamp after all filters; mirror that
	// boundary here for every retained state so modes and their payload fields
	// cannot be lost in the ID -> Entry handoff.
	discovery.StampEntriesWithScopeOutputMode(view.Entries, view.Scope.OutputMode(), view.Scope)
	view.Discovered.Scope = view.Scope
	view.Discovered.GitContext = view.GitContext
	view.Discovered.Entries = append([]discovery.Entry(nil), view.Entries...)
	return view
}

func cloneSnippetLineOverlay(in map[uint32][]int) map[uint32][]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[uint32][]int, len(in))
	for id, lines := range in {
		out[id] = append([]int(nil), lines...)
	}
	return out
}

func cloneExecutionScopes(in []command.ExecutionScope) []command.ExecutionScope {
	if len(in) == 0 {
		return nil
	}
	out := make([]command.ExecutionScope, len(in))
	for i := range in {
		out[i] = cloneExecutionScope(in[i])
	}
	return out
}

func cloneExecutionScope(in command.ExecutionScope) command.ExecutionScope {
	out := in
	out.Targets = cloneStringSlice(in.Targets)
	out.Only = cloneStringSlice(in.Only)
	out.Exclude = cloneStringSlice(in.Exclude)
	out.NotContains = cloneStringSlice(in.NotContains)
	out.Stages = cloneCommandStages(in.Stages)
	return out
}

func cloneCommandStages(in []command.Stage) []command.Stage {
	if len(in) == 0 {
		return nil
	}
	out := make([]command.Stage, len(in))
	for i, stage := range in {
		out[i] = stage
		out[i].Values = cloneStringSlice(stage.Values)
		out[i].Nums = append([]int(nil), stage.Nums...)
		if stage.Limit != nil {
			limit := *stage.Limit
			out[i].Limit = &limit
		}
	}
	return out
}

func cloneDiscoveryEntries(in []discovery.Entry) []discovery.Entry {
	if len(in) == 0 {
		return nil
	}
	out := append([]discovery.Entry(nil), in...)
	for i := range out {
		out[i].SnippetMatchLines = append([]int(nil), in[i].SnippetMatchLines...)
	}
	return out
}

func (inventory *scopeViewInventory) cachedScopedIgnored(compute func() (bool, error)) (bool, bool, error) {
	if inventory == nil {
		value, err := compute()
		return value, false, err
	}
	inventory.observationMu.Lock()
	defer inventory.observationMu.Unlock()
	if inventory.scopedIgnoredKnown {
		return inventory.hasScopedIgnored, true, nil
	}
	value, err := compute()
	if err != nil {
		return false, false, err
	}
	inventory.hasScopedIgnored = value
	inventory.scopedIgnoredKnown = true
	return value, false, nil
}

func (inventory *scopeViewInventory) cachedGitStatus(current []discovery.Entry, compute func([]discovery.Entry) (map[string]string, error)) (map[string]string, bool, error) {
	if inventory == nil {
		value, err := compute(current)
		return value, false, err
	}
	inventory.observationMu.Lock()
	defer inventory.observationMu.Unlock()
	if inventory.gitStatusKnown {
		return cloneStringMap(inventory.gitStatus), true, nil
	}
	entries := current
	inventory.mu.RLock()
	if !sameEntryOrder(entries, inventory.entries) {
		entries = append([]discovery.Entry(nil), inventory.entries...)
	}
	inventory.mu.RUnlock()
	value, err := compute(entries)
	if err != nil {
		return nil, false, err
	}
	// The inventory owns its cached observation. Neither the compute callback
	// nor a caller receiving this result may retain a mutable alias to it.
	inventory.gitStatus = cloneStringMap(value)
	inventory.gitStatusKnown = true
	return cloneStringMap(inventory.gitStatus), false, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func sameEntryOrder(left, right []discovery.Entry) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].RelPath != right[i].RelPath {
			return false
		}
	}
	return true
}

func scopeViewMemoReset() {
	scopeViewMemoMu.Lock()
	dirs := make([]string, 0, len(scopeViewMemoValues)*2)
	for _, entry := range scopeViewMemoValues {
		if entry.checkpointDir != "" {
			dirs = append(dirs, entry.checkpointDir)
		}
		if entry.targetPreviewInventoryDir != "" {
			dirs = append(dirs, entry.targetPreviewInventoryDir)
		}
	}
	scopeViewMemoValues = make(map[string]scopeViewMemoEntry)
	scopeViewMemoNextID = 0
	scopeViewMemoTargetMetadata = nil
	scopeViewMemoGenerationSealed = false
	scopeViewMemoContentStages = make(map[string]retainedContentStageResult)
	scopeViewMemoCond.Broadcast()
	scopeViewMemoMu.Unlock()
	for _, dir := range dirs {
		_ = os.RemoveAll(dir)
	}
}

// scopeViewMemoAdoptContentStage retains the exact survivor set produced by
// the live content picker. The key is the argv state after adding the content
// stage, so only that exact transition can consume it. Paths are immutable and
// normalized once here; derivation only performs ordered membership checks.
func scopeViewMemoAdoptContentStage(args []string, kind command.StageKind, pattern string, absPaths []string) {
	if (kind != command.StageContains && kind != command.StageNotContains) || pattern == "" {
		return
	}
	_, key := scopeViewMemoKey(args)
	survivors := make(map[string]struct{}, len(absPaths))
	for _, path := range absPaths {
		if clean := filepath.Clean(path); clean != "." && clean != "" {
			survivors[clean] = struct{}{}
		}
	}
	scopeViewMemoMu.Lock()
	scopeViewMemoContentStages[key] = retainedContentStageResult{
		kind:      kind,
		pattern:   pattern,
		survivors: survivors,
	}
	scopeViewMemoMu.Unlock()
}

// scopeViewMemoAdoptSnippetStage retains both membership and match-line
// offsets from the boundary picker's single content scan. File contents remain
// uncached; the offsets are the minimum mode-specific state needed by output.
func scopeViewMemoAdoptSnippetStage(args []string, pattern string, entries []discovery.Entry) {
	if pattern == "" {
		return
	}
	_, key := scopeViewMemoKey(args)
	result := retainedContentStageResult{
		kind:         command.StageSnippet,
		pattern:      pattern,
		survivors:    make(map[string]struct{}, len(entries)),
		snippetLines: make(map[string][]int, len(entries)),
	}
	for _, entry := range entries {
		if entry.AbsPath == "" || len(entry.SnippetMatchLines) == 0 {
			continue
		}
		path := filepath.Clean(entry.AbsPath)
		result.survivors[path] = struct{}{}
		result.snippetLines[path] = append([]int(nil), entry.SnippetMatchLines...)
	}
	scopeViewMemoMu.Lock()
	scopeViewMemoContentStages[key] = result
	scopeViewMemoMu.Unlock()
}

func scopeViewMemoTakeContentStage(key string, stage command.Stage) (retainedContentStageResult, bool) {
	if len(stage.Values) != 1 {
		return retainedContentStageResult{}, false
	}
	scopeViewMemoMu.Lock()
	result, ok := scopeViewMemoContentStages[key]
	if ok && result.kind == stage.Kind && result.pattern == stage.Values[0] {
		delete(scopeViewMemoContentStages, key)
	} else {
		ok = false
	}
	scopeViewMemoMu.Unlock()
	return result, ok
}

// scopeViewMemoAdoptTargetMetadata publishes the target picker's completed
// Lstat records to later filter and output states. It is called after the
// picker closes, so no worker can mutate the source capture concurrently.
func scopeViewMemoAdoptTargetMetadata(metadata map[string]search.FileMetadata) {
	if len(metadata) == 0 {
		return
	}
	scopeViewMemoMu.Lock()
	if scopeViewMemoTargetMetadata == nil {
		// MetadataSnapshot already returned a detached, immutable map. Adopt it
		// directly so a large target universe is copied only once on Enter.
		scopeViewMemoTargetMetadata = metadata
		scopeViewMemoMu.Unlock()
		return
	}
	for path, record := range metadata {
		scopeViewMemoTargetMetadata[path] = record
	}
	scopeViewMemoMu.Unlock()
}

// scopeViewMemoAdoptTargetSelection installs the target picker's committed
// membership as the base state for the exact argv generation. Parsing remains
// authoritative for command semantics; only discovery and primary metadata
// come from the already-completed picker work.
func scopeViewMemoAdoptTargetSelection(args []string, gitCtx git.Context, entries []discovery.Entry, metadata map[string]search.FileMetadata, targetPreviewInventoryPath ...string) bool {
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return false
	}
	invocation := command.ResolvedFromParsed(cfg)
	if len(invocation.Scopes) == 0 {
		return false
	}
	scopeIndex := len(invocation.Scopes) - 1
	currentScope := invocation.Scopes[scopeIndex]
	selected := cloneDiscoveryEntries(entries)
	discovery.StampEntriesWithScopeOutputMode(selected, currentScope.OutputMode(), currentScope)
	selected = discovery.EnsureEntryAbsPaths(selected, invocation.Config.WorkingDir)

	scopeViewMemoAdoptTargetMetadata(metadata)
	_, key := scopeViewMemoKey(args)
	view := resolvedScopeView{
		Invocation: invocation.Config,
		Render:     RenderConfigFromParsedCommand(cfg),
		Progress:   interactiveProgressExtrasFromParsed(cfg),
		GitContext: gitCtx,
		Scopes:     cloneExecutionScopes(invocation.Scopes),
		ScopeIndex: scopeIndex,
		Scope:      cloneExecutionScope(currentScope),
		Entries:    selected,
		Discovered: discovery.Scope{
			Scope:      cloneExecutionScope(currentScope),
			GitContext: gitCtx,
			Entries:    cloneDiscoveryEntries(selected),
		},
	}
	stored := scopeViewMemoStoreFresh(key, args, view)
	scopeViewMemoMu.Lock()
	current, ok := scopeViewMemoValues[key]
	if ok && current.stateID == stored.stateID {
		current.targetSelectionBase = true
		if len(targetPreviewInventoryPath) > 0 && targetPreviewInventoryPath[0] != "" {
			current.targetPreviewInventoryPath = targetPreviewInventoryPath[0]
		}
		scopeViewMemoValues[key] = current
		stored = current
	}
	scopeViewMemoMu.Unlock()
	sealed := stored.inventory != nil && stored.inventory.metadataSealed
	if sealed {
		scopeViewMemoMu.Lock()
		scopeViewMemoGenerationSealed = true
		scopeViewMemoMu.Unlock()
	}
	return sealed
}

func scopeViewMemoTargetPreviewInventoryPath(args []string) (string, bool) {
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	entry, ok := scopeViewMemoValues[key]
	path := entry.targetPreviewInventoryPath
	scopeViewMemoMu.Unlock()
	return path, ok && path != ""
}

func scopeViewMemoTargetPreviewInventory(args []string) (string, bool, error) {
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	var entry scopeViewMemoEntry
	for {
		var ok bool
		entry, ok = scopeViewMemoValues[key]
		if !ok || !entry.targetSelectionBase {
			scopeViewMemoMu.Unlock()
			return "", false, nil
		}
		if entry.targetPreviewInventoryPath != "" {
			path := entry.targetPreviewInventoryPath
			scopeViewMemoMu.Unlock()
			return path, true, nil
		}
		if !entry.targetPreviewInventoryBusy {
			entry.targetPreviewInventoryBusy = true
			scopeViewMemoValues[key] = entry
			break
		}
		scopeViewMemoCond.Wait()
	}
	scopeViewMemoMu.Unlock()

	if entry.inventory == nil || len(entry.fileIDs) != len(entry.inventory.entries) {
		scopeViewMemoTargetPreviewInventoryBuildFailed(key, entry.stateID)
		return "", false, nil
	}
	entry.inventory.mu.RLock()
	for index, id := range entry.fileIDs {
		if uint64(id) >= uint64(len(entry.inventory.entries)) || int(id) != index {
			entry.inventory.mu.RUnlock()
			scopeViewMemoTargetPreviewInventoryBuildFailed(key, entry.stateID)
			return "", false, nil
		}
		candidate := entry.inventory.entries[id]
		if !candidate.SizeKnown {
			entry.inventory.mu.RUnlock()
			scopeViewMemoTargetPreviewInventoryBuildFailed(key, entry.stateID)
			return "", false, nil
		}
	}
	tmpdir, err := os.MkdirTemp("", "catclip-session-target-state-*")
	if err != nil {
		entry.inventory.mu.RUnlock()
		scopeViewMemoTargetPreviewInventoryBuildFailed(key, entry.stateID)
		return "", true, err
	}
	inventoryPath := filepath.Join(tmpdir, "targets.bin")
	finishWriteBench := platform.InternalBenchSpan("ui.resolved_scope_view.target_inventory_write",
		"state_id", platform.InternalBenchInt(int(entry.stateID)),
		"entries", platform.InternalBenchInt(len(entry.fileIDs)),
	)
	err = discovery.WriteTargetPreviewEntryInventory(inventoryPath, entry.view.GitContext, entry.inventory.entries)
	entry.inventory.mu.RUnlock()
	finishWriteBench("err", platform.InternalBenchError(err))
	if err != nil {
		_ = os.RemoveAll(tmpdir)
		scopeViewMemoTargetPreviewInventoryBuildFailed(key, entry.stateID)
		return "", true, err
	}

	scopeViewMemoMu.Lock()
	current, stillPresent := scopeViewMemoValues[key]
	if !stillPresent || current.stateID != entry.stateID {
		scopeViewMemoCond.Broadcast()
		scopeViewMemoMu.Unlock()
		_ = os.RemoveAll(tmpdir)
		return "", false, nil
	}
	current.targetPreviewInventoryPath = inventoryPath
	current.targetPreviewInventoryDir = tmpdir
	current.targetPreviewInventoryBusy = false
	scopeViewMemoValues[key] = current
	scopeViewMemoCond.Broadcast()
	scopeViewMemoMu.Unlock()
	return inventoryPath, true, nil
}

func scopeViewMemoTargetPreviewInventoryBuildFailed(key string, stateID uint64) {
	scopeViewMemoMu.Lock()
	current, ok := scopeViewMemoValues[key]
	if ok && current.stateID == stateID {
		current.targetPreviewInventoryBusy = false
		scopeViewMemoValues[key] = current
	}
	scopeViewMemoCond.Broadcast()
	scopeViewMemoMu.Unlock()
}

func scopeViewMemoHasSealedGeneration() bool {
	scopeViewMemoMu.Lock()
	sealed := scopeViewMemoGenerationSealed
	scopeViewMemoMu.Unlock()
	return sealed
}

// scopeViewMemoSealEvaluatedTarget is the authorized fallback for an
// interactive target frame that cannot be projected from the visible picker
// inventory (for example, an exact ignored root). It evaluates that new target
// universe once, completes its primary metadata, and seals the resulting state
// before any filter or output screen can open.
func scopeViewMemoSealEvaluatedTarget(args []string) error {
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		return err
	}
	if _, ok := retainedScopeViewEntriesWithMetadata(view); !ok {
		return errors.New("internal error: target scope metadata could not be sealed")
	}
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	entry, ok := scopeViewMemoValues[key]
	scopeViewMemoMu.Unlock()
	sealed := false
	if ok && entry.inventory != nil {
		entry.inventory.mu.RLock()
		sealed = entry.inventory.metadataSealed
		entry.inventory.mu.RUnlock()
	}
	if sealed {
		scopeViewMemoMu.Lock()
		scopeViewMemoGenerationSealed = true
		scopeViewMemoMu.Unlock()
	}
	if !sealed {
		return errors.New("internal error: target scope could not be sealed")
	}
	return nil
}

func scopeViewMemoLookup(key string) (scopeViewMemoEntry, bool) {
	scopeViewMemoMu.Lock()
	entry, ok := scopeViewMemoValues[key]
	if !ok {
		scopeViewMemoMu.Unlock()
		return scopeViewMemoEntry{}, false
	}
	entry.args = append([]string(nil), entry.args...)
	entry.fileIDs = append([]uint32(nil), entry.fileIDs...)
	scopeViewMemoMu.Unlock()
	entry.view = materializeScopeView(entry)
	return entry, true
}

func scopeViewMemoStoreFresh(key string, args []string, view resolvedScopeView) scopeViewMemoEntry {
	scopeViewMemoMu.Lock()
	defer scopeViewMemoMu.Unlock()
	if existing, ok := scopeViewMemoValues[key]; ok {
		return existing
	}
	scopeViewMemoNextID++
	inventory := &scopeViewInventory{
		entries:             append([]discovery.Entry(nil), view.Entries...),
		initialSizeKnown:    make([]bool, len(view.Entries)),
		initialModTimeKnown: make([]bool, len(view.Entries)),
		metadata:            make([]search.FileMetadata, len(view.Entries)),
		metadataKnown:       make([]bool, len(view.Entries)),
		metadataSealed:      true,
	}
	fileIDs := make([]uint32, len(inventory.entries))
	for i := range fileIDs {
		fileIDs[i] = uint32(i)
		inventory.entries[i].SnippetMatchLines = append([]int(nil), inventory.entries[i].SnippetMatchLines...)
		if metadata, ok := scopeViewMemoTargetMetadata[inventory.entries[i].RelPath]; ok {
			inventory.metadata[i] = metadata
			inventory.metadataKnown[i] = true
			if metadata.State == search.FileMetadataReady {
				inventory.entries[i].SizeBytes = metadata.SizeBytes
				inventory.entries[i].SizeKnown = true
				inventory.entries[i].ModTime = metadata.ModTime
			}
		} else {
			inventory.metadataSealed = false
		}
		inventory.initialSizeKnown[i] = inventory.entries[i].SizeKnown
		inventory.initialModTimeKnown[i] = !inventory.entries[i].ModTime.IsZero()
	}
	view.Entries = nil
	view.Discovered.Entries = nil
	entry := scopeViewMemoEntry{
		inventory:         inventory,
		fileIDs:           fileIDs,
		args:              append([]string(nil), args...),
		view:              view,
		stateID:           scopeViewMemoNextID,
		projectMetadata:   scopeProjectsMetadata(view.Scope),
		snippetMatchLines: snippetLineOverlayForEntries(inventory.entries, fileIDs),
	}
	scopeViewMemoValues[key] = entry
	return entry
}

func scopeViewMemoStoreDerived(key string, args []string, view resolvedScopeView, parent scopeViewMemoEntry, fileIDs []uint32) scopeViewMemoEntry {
	scopeViewMemoMu.Lock()
	defer scopeViewMemoMu.Unlock()
	if existing, ok := scopeViewMemoValues[key]; ok {
		return existing
	}
	scopeViewMemoNextID++
	view.Entries = nil
	view.Discovered.Entries = nil
	entry := scopeViewMemoEntry{
		inventory:         parent.inventory,
		fileIDs:           append([]uint32(nil), fileIDs...),
		args:              append([]string(nil), args...),
		view:              view,
		stateID:           scopeViewMemoNextID,
		parentID:          parent.stateID,
		projectMetadata:   parent.projectMetadata || scopeProjectsMetadata(view.Scope),
		snippetMatchLines: cloneSnippetLineOverlay(view.snippetMatchLines),
	}
	scopeViewMemoValues[key] = entry
	return entry
}

func snippetLineOverlayForEntries(entries []discovery.Entry, ids []uint32) map[uint32][]int {
	var out map[uint32][]int
	for i, id := range ids {
		if i >= len(entries) || len(entries[i].SnippetMatchLines) == 0 {
			continue
		}
		if out == nil {
			out = make(map[uint32][]int)
		}
		out[id] = append([]int(nil), entries[i].SnippetMatchLines...)
	}
	return out
}

func scopeProjectsMetadata(scope command.ExecutionScope) bool {
	for _, stage := range scope.Stages {
		if stage.Kind == command.StageRecent || stage.Kind == command.StageSize {
			return true
		}
	}
	return false
}

func scopeViewMemoLongestPrefix(wd string, args []string) (scopeViewMemoEntry, bool) {
	prefix := wd + "\x00\x00"
	scopeViewMemoMu.Lock()
	defer scopeViewMemoMu.Unlock()
	var best scopeViewMemoEntry
	found := false
	for key, entry := range scopeViewMemoValues {
		if !strings.HasPrefix(key, prefix) || len(entry.args) >= len(args) || len(entry.args) <= len(best.args) {
			continue
		}
		if reflect.DeepEqual(args[:len(entry.args)], entry.args) {
			best = entry
			found = true
		}
	}
	if !found {
		return scopeViewMemoEntry{}, false
	}
	best.args = append([]string(nil), best.args...)
	best.fileIDs = append([]uint32(nil), best.fileIDs...)
	return best, true
}

func scopeViewMemoCanAdvanceTo(wd string, args []string, requested command.Invocation) bool {
	parent, ok := scopeViewMemoLongestPrefix(wd, args)
	return ok && scopeViewInvocationCompatible(parent.view.Invocation, requested)
}

// scopeViewMemoDiscoveryResult assembles every resolved --then scope from the
// immutable states retained by this interactive invocation. It is all-or-
// nothing: a missing or incompatible leg makes the caller use canonical
// discovery for the complete invocation, never a mixture of retained and
// freshly walked scopes.
func scopeViewMemoDiscoveryResult(requested command.Resolved) (discovery.Result, git.Context, bool) {
	if len(requested.Scopes) == 0 {
		return discovery.Result{}, git.Context{}, false
	}

	scopeViewMemoMu.Lock()
	selected := make([]scopeViewMemoEntry, len(requested.Scopes))
	found := make([]bool, len(requested.Scopes))
	for _, candidate := range scopeViewMemoValues {
		if !scopeViewInvocationCompatible(candidate.view.Invocation, requested.Config) {
			continue
		}
		for i, scope := range requested.Scopes {
			if !reflect.DeepEqual(candidate.view.Scope, scope) {
				continue
			}
			if !found[i] || candidate.stateID > selected[i].stateID {
				selected[i] = candidate
				found[i] = true
			}
		}
	}
	for i := range found {
		if !found[i] {
			scopeViewMemoMu.Unlock()
			return discovery.Result{}, git.Context{}, false
		}
		selected[i].fileIDs = append([]uint32(nil), selected[i].fileIDs...)
	}
	scopeViewMemoMu.Unlock()

	result := discovery.Result{
		Invocation: discovery.Discovered{
			Config: requested.Config,
			Scopes: make([]discovery.Scope, 0, len(selected)),
		},
		ScopeStats: make([]discovery.ScopeStat, 0, len(selected)),
	}
	gitCtx := selected[0].view.GitContext
	for i, entry := range selected {
		view := materializeScopeViewWithMetadata(entry, true)
		if !reflect.DeepEqual(view.GitContext, gitCtx) {
			return discovery.Result{}, git.Context{}, false
		}
		scope := view.Discovered
		scope.Scope = requested.Scopes[i]
		scope.GitContext = gitCtx
		scope.Entries = append([]discovery.Entry(nil), view.Entries...)
		scope.Diagnostics = append([]discovery.Diagnostic(nil), scope.Diagnostics...)
		for j := range scope.Diagnostics {
			scope.Diagnostics[j].ScopeIndex = i
		}
		scope.Notices = append([]string(nil), scope.Notices...)
		result.Invocation.Scopes = append(result.Invocation.Scopes, scope)
		result.Diagnostics = append(result.Diagnostics, scope.Diagnostics...)
		result.Notices = append(result.Notices, scope.Notices...)
		result.HadSelectionCancel = result.HadSelectionCancel || scope.SelectionCancel
		result.ScopeStats = append(result.ScopeStats, discovery.ScopeStat{
			Index:    i,
			Count:    len(scope.Entries),
			Duration: view.Duration,
		})
	}
	return result, gitCtx, true
}

func scopeViewInvocationCompatible(retained, requested command.Invocation) bool {
	return retained.WorkingDir == requested.WorkingDir &&
		retained.Platform == requested.Platform &&
		retained.WithBinaries == requested.WithBinaries &&
		retained.Internal == requested.Internal
}

func scopeViewMemoKey(args []string) (string, string) {
	wd, err := os.Getwd()
	if err != nil {
		wd = ""
	}
	return wd, wd + "\x00\x00" + strings.Join(args, "\x00")
}

// scopeViewMemoCheckpoint materializes one checkpoint per immutable state.
// Different picker kinds build different preview commands around the same
// file. The artifact lives until the interactive invocation ends so undo can
// reuse it without another metadata pass or JSON write.
func scopeViewMemoCheckpointPath(args []string) (string, bool) {
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	entry, ok := scopeViewMemoValues[key]
	if !ok || entry.checkpointPath == "" {
		scopeViewMemoMu.Unlock()
		return "", false
	}
	path := entry.checkpointPath
	scopeViewMemoMu.Unlock()
	finishBench := platform.InternalBenchSpan("ui.resolved_scope_view.checkpoint_hit",
		"state_id", platform.InternalBenchInt(int(entry.stateID)),
		"entries", platform.InternalBenchInt(len(entry.fileIDs)),
	)
	finishBench("err", "false")
	return path, true
}

func scopeViewMemoCheckpoint(args []string, view resolvedScopeView, statuses map[string]string) (string, bool, error) {
	_, key := scopeViewMemoKey(args)
	scopeViewMemoMu.Lock()
	var entry scopeViewMemoEntry
	for {
		var ok bool
		entry, ok = scopeViewMemoValues[key]
		if !ok {
			scopeViewMemoMu.Unlock()
			return "", false, nil
		}
		if entry.checkpointPath != "" {
			path := entry.checkpointPath
			scopeViewMemoMu.Unlock()
			finishBench := platform.InternalBenchSpan("ui.resolved_scope_view.checkpoint_hit",
				"state_id", platform.InternalBenchInt(int(entry.stateID)),
				"entries", platform.InternalBenchInt(len(entry.fileIDs)),
			)
			finishBench("err", "false")
			return path, true, nil
		}
		if len(view.Entries) != len(entry.fileIDs) {
			scopeViewMemoMu.Unlock()
			return "", false, nil
		}
		if !entry.checkpointBusy {
			entry.checkpointBusy = true
			scopeViewMemoValues[key] = entry
			break
		}
		scopeViewMemoCond.Wait()
	}
	scopeViewMemoMu.Unlock()
	expectedView := materializeScopeView(entry)
	if !sameCheckpointScopeProjection(view, expectedView) {
		scopeViewMemoCheckpointBuildFailed(key, entry.stateID)
		return "", false, nil
	}
	if entry.inventory == nil {
		scopeViewMemoCheckpointBuildFailed(key, entry.stateID)
		return "", false, nil
	}
	finishSizeBench := platform.InternalBenchSpan("ui.resolved_scope_view.checkpoint_sizes",
		"state_id", platform.InternalBenchInt(int(entry.stateID)),
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	var metadataOK bool
	view.Entries, metadataOK = retainedScopeViewEntriesWithMetadata(view)
	finishSizeBench("err", "false")
	if !metadataOK {
		scopeViewMemoCheckpointBuildFailed(key, entry.stateID)
		return "", false, nil
	}

	tmpdir, err := os.MkdirTemp("", "catclip-session-state-*")
	if err != nil {
		scopeViewMemoCheckpointBuildFailed(key, entry.stateID)
		return "", true, err
	}
	checkpointPath := filepath.Join(tmpdir, "scope.json")
	finishWriteBench := platform.InternalBenchSpan("ui.resolved_scope_view.checkpoint_write",
		"state_id", platform.InternalBenchInt(int(entry.stateID)),
		"entries", platform.InternalBenchInt(len(view.Entries)),
	)
	err = discovery.WriteCheckpoint(checkpointPath, view.Invocation.WorkingDir, discovery.CheckpointData{
		GitContext: view.GitContext,
		GitStatus:  statuses,
		Entries:    view.Entries,
		NoIgnore:   view.Scope.NoIgnore,
	})
	finishWriteBench("err", platform.InternalBenchError(err))
	if err != nil {
		_ = os.RemoveAll(tmpdir)
		scopeViewMemoCheckpointBuildFailed(key, entry.stateID)
		return "", true, err
	}

	scopeViewMemoMu.Lock()
	current, stillPresent := scopeViewMemoValues[key]
	if !stillPresent || current.stateID != entry.stateID {
		scopeViewMemoCond.Broadcast()
		scopeViewMemoMu.Unlock()
		_ = os.RemoveAll(tmpdir)
		return "", false, nil
	}
	current.checkpointPath = checkpointPath
	current.checkpointDir = tmpdir
	current.checkpointBusy = false
	scopeViewMemoValues[key] = current
	scopeViewMemoCond.Broadcast()
	scopeViewMemoMu.Unlock()
	return checkpointPath, true, nil
}

func sameCheckpointScopeProjection(got, want resolvedScopeView) bool {
	if !reflect.DeepEqual(got.Invocation, want.Invocation) ||
		!reflect.DeepEqual(got.GitContext, want.GitContext) ||
		got.ScopeIndex != want.ScopeIndex ||
		!reflect.DeepEqual(got.Scope, want.Scope) ||
		!reflect.DeepEqual(got.Scopes, want.Scopes) ||
		len(got.Entries) != len(want.Entries) {
		return false
	}
	for i := range got.Entries {
		left := got.Entries[i]
		right := want.Entries[i]
		// Metadata can be captured monotonically after the caller materializes
		// its view. It is overlaid from the inventory before serialization, so
		// exclude only those observation fields from the identity/projection
		// comparison. Every selection and output-shape field remains exact.
		left.AbsPath, right.AbsPath = "", ""
		left.ModTime, right.ModTime = time.Time{}, time.Time{}
		left.SizeBytes, right.SizeBytes = 0, 0
		left.SizeKnown, right.SizeKnown = false, false
		if !reflect.DeepEqual(left, right) {
			return false
		}
	}
	return true
}

// retainedScopeViewEntriesWithMetadata overlays the shared metadata records,
// fills only IDs that are still missing, and publishes those first successful
// lookups back to the inventory. metadataMu makes this a per-inventory
// single-flight operation across checkpoints and specialized picker setup.
func retainedScopeViewEntriesWithMetadata(view resolvedScopeView) ([]discovery.Entry, bool) {
	if view.inventory == nil || len(view.Entries) != len(view.fileIDs) {
		return nil, false
	}
	inventory := view.inventory
	inventory.metadataMu.Lock()
	defer inventory.metadataMu.Unlock()
	if inventory.metadataSealed {
		entries := append([]discovery.Entry(nil), view.Entries...)
		inventory.mu.RLock()
		defer inventory.mu.RUnlock()
		for i, id := range view.fileIDs {
			if uint64(id) >= uint64(len(inventory.entries)) || !inventory.metadataKnown[id] {
				return nil, false
			}
			record := inventory.metadata[id]
			if record.State == search.FileMetadataReady {
				entries[i].SizeBytes = record.SizeBytes
				entries[i].SizeKnown = true
				entries[i].ModTime = record.ModTime
			}
		}
		return entries, true
	}

	entries := append([]discovery.Entry(nil), view.Entries...)
	missing := make([]string, 0, len(view.fileIDs))
	inventory.mu.RLock()
	for i, id := range view.fileIDs {
		if uint64(id) >= uint64(len(inventory.entries)) || entries[i].RelPath != inventory.entries[id].RelPath {
			inventory.mu.RUnlock()
			return nil, false
		}
		if !inventory.metadataKnown[id] {
			missing = append(missing, inventory.entries[id].RelPath)
		}
	}
	inventory.mu.RUnlock()

	// A checkpoint is the completion boundary for a generation. Capture full
	// terminal metadata records here instead of only filling successful sizes;
	// later filters can then distinguish a known failure from an unobserved path
	// without issuing another Lstat.
	capture := search.StartTextSizeCapture(view.Invocation.WorkingDir, missing)
	<-capture.Done()
	records := capture.MetadataSnapshot()
	inventory.mu.Lock()
	for _, id := range view.fileIDs {
		if uint64(id) >= uint64(len(inventory.entries)) || inventory.metadataKnown[id] {
			continue
		}
		record, ok := records[inventory.entries[id].RelPath]
		if !ok {
			inventory.mu.Unlock()
			return nil, false
		}
		inventory.metadata[id] = record
		inventory.metadataKnown[id] = true
		if record.State == search.FileMetadataReady {
			inventory.entries[id].SizeBytes = record.SizeBytes
			inventory.entries[id].SizeKnown = true
			inventory.entries[id].ModTime = record.ModTime
		}
	}
	inventory.metadataSealed = allMetadataKnown(inventory.metadataKnown)
	for i, id := range view.fileIDs {
		if uint64(id) >= uint64(len(inventory.entries)) {
			continue
		}
		record := inventory.metadata[id]
		if record.State == search.FileMetadataReady {
			entries[i].SizeBytes = record.SizeBytes
			entries[i].SizeKnown = true
			entries[i].ModTime = record.ModTime
		}
	}
	inventory.mu.Unlock()
	return entries, true
}

// retainedScopeViewEntriesWithReadyMetadata is the metadata-filter boundary:
// it never retries a terminal failed Lstat. A vanished or unreadable record is
// returned as the original observation error instead of being passed to a
// helper that would call Lstat again.
func retainedScopeViewEntriesWithReadyMetadata(view resolvedScopeView) ([]discovery.Entry, bool, error) {
	entries, ok := retainedScopeViewEntriesWithMetadata(view)
	if !ok || view.inventory == nil {
		return nil, ok, nil
	}
	view.inventory.mu.RLock()
	defer view.inventory.mu.RUnlock()
	for _, id := range view.fileIDs {
		if uint64(id) >= uint64(len(view.inventory.entries)) || !view.inventory.metadataKnown[id] {
			return nil, false, nil
		}
		record := view.inventory.metadata[id]
		if record.State == search.FileMetadataReady {
			continue
		}
		path := view.inventory.entries[id].AbsPath
		if path == "" {
			path = filepath.Join(view.Invocation.WorkingDir, filepath.FromSlash(view.inventory.entries[id].RelPath))
		}
		cause := os.ErrNotExist
		if record.Error != "" {
			cause = errors.New(record.Error)
		}
		return nil, true, &os.PathError{Op: "lstat", Path: path, Err: cause}
	}
	return entries, true, nil
}

func allMetadataKnown(known []bool) bool {
	for _, ok := range known {
		if !ok {
			return false
		}
	}
	return true
}

func scopeViewMemoCheckpointBuildFailed(key string, stateID uint64) {
	scopeViewMemoMu.Lock()
	if current, ok := scopeViewMemoValues[key]; ok && current.stateID == stateID {
		current.checkpointBusy = false
		scopeViewMemoValues[key] = current
	}
	scopeViewMemoCond.Broadcast()
	scopeViewMemoMu.Unlock()
}

func resolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, error) {
	// The working directory joins the key: args alone are ambiguous
	// across cwds (a production process never chdirs, but the test
	// binary does, and correctness shouldn't rely on that).
	wd, key := scopeViewMemoKey(args)
	if entry, ok := scopeViewMemoLookup(key); ok {
		finishBench := platform.InternalBenchSpan("ui.resolved_scope_view.memo_hit",
			"entries", platform.InternalBenchInt(len(entry.view.Entries)),
			"state_id", platform.InternalBenchInt(int(entry.stateID)),
		)
		finishBench("err", "false")
		return entry.view, nil
	}
	cfg, err := cli.ParseArgsAllowImplicitDot(args)
	if err != nil {
		return resolvedScopeView{}, err
	}
	invocation := command.ResolvedFromParsed(cfg)
	if parent, ok := scopeViewMemoLongestPrefix(wd, args); ok {
		finishBench := platform.InternalBenchSpan("ui.resolved_scope_view.derived",
			"parent_state_id", platform.InternalBenchInt(int(parent.stateID)),
		)
		if view, fileIDs, derived, deriveErr := deriveScopeViewFromParent(parent, invocation, RenderConfigFromParsedCommand(cfg), key); derived {
			if deriveErr != nil {
				finishBench(
					"eligible", "true",
					"entries", platform.InternalBenchInt(len(fileIDs)),
					"err", platform.InternalBenchError(deriveErr),
				)
				return resolvedScopeView{}, deriveErr
			}
			view.Progress = interactiveProgressExtrasFromParsed(cfg)
			stored := scopeViewMemoStoreDerived(key, args, view, parent, fileIDs)
			finishBench(
				"eligible", "true",
				"entries", platform.InternalBenchInt(len(fileIDs)),
				"err", "false",
				"state_id", platform.InternalBenchInt(int(stored.stateID)),
			)
			return materializeScopeView(stored), nil
		}
		finishBench("eligible", "false", "err", "false")
	}
	view, err := resolvedCurrentScopeView(invocation, RenderConfigFromParsedCommand(cfg))
	if err != nil {
		return view, err
	}
	view.Progress = interactiveProgressExtrasFromParsed(cfg)
	stored := scopeViewMemoStoreFresh(key, args, view)
	return materializeScopeView(stored), nil
}

func deriveScopeViewFromParent(parent scopeViewMemoEntry, requested command.Resolved, renderCfg RenderConfig, requestedKey string) (resolvedScopeView, []uint32, bool, error) {
	if len(parent.view.Scopes) == 0 || len(parent.view.Scopes) != len(requested.Scopes) || !reflect.DeepEqual(parent.view.Invocation, requested.Config) {
		return resolvedScopeView{}, nil, false, nil
	}
	last := len(requested.Scopes) - 1
	if !reflect.DeepEqual(parent.view.Scopes[:last], requested.Scopes[:last]) {
		return resolvedScopeView{}, nil, false, nil
	}
	parentScope := parent.view.Scopes[last]
	requestedScope := requested.Scopes[last]
	if len(requestedScope.Stages) != len(parentScope.Stages)+1 {
		return resolvedScopeView{}, nil, false, nil
	}
	requestedPrefix := append([]command.Stage(nil), requestedScope.Stages[:len(parentScope.Stages)]...)
	if !reflect.DeepEqual(requestedPrefix, parentScope.Stages) {
		return resolvedScopeView{}, nil, false, nil
	}
	stage := requestedScope.Stages[len(requestedScope.Stages)-1]
	expected := parentScope
	expected.Stages = append(append([]command.Stage(nil), parentScope.Stages...), stage)
	switch stage.Kind {
	case command.StageNoIgnore:
		expected.NoIgnore = true
	case command.StageOnly:
		expected.Only = append(append([]string(nil), parentScope.Only...), stage.Values...)
	case command.StageExclude:
		expected.Exclude = append(append([]string(nil), parentScope.Exclude...), stage.Values...)
	case command.StageContains:
		if len(stage.Values) != 1 {
			return resolvedScopeView{}, nil, false, nil
		}
		expected.Contains = stage.Values[0]
	case command.StageNotContains:
		if len(stage.Values) != 1 {
			return resolvedScopeView{}, nil, false, nil
		}
		expected.NotContains = append(append([]string(nil), parentScope.NotContains...), stage.Values[0])
	case command.StageSnippet:
		if len(stage.Values) != 1 {
			return resolvedScopeView{}, nil, false, nil
		}
		expected.Snippet = true
		expected.SnippetPattern = stage.Values[0]
		expected.SnippetContextSet = requestedScope.SnippetContextSet
		expected.SnippetContextLines = requestedScope.SnippetContextLines
	case command.StageChanged:
		expected.Changed = true
	case command.StageStaged:
		expected.Changed = true
		expected.Staged = true
	case command.StageUnstaged:
		expected.Changed = true
		expected.Unstaged = true
	case command.StageUntracked:
		expected.Changed = true
		expected.Untracked = true
	case command.StageChangedDiff:
		expected.Changed = true
		expected.Diff = true
	case command.StageStagedDiff:
		expected.Changed = true
		expected.Staged = true
		expected.Diff = true
	case command.StageUnstagedDiff:
		expected.Changed = true
		expected.Unstaged = true
		expected.Diff = true
	case command.StageDepth:
	case command.StagePaths:
		expected.Paths = true
	case command.StageLines:
		expected.Lines = true
		expected.LinesStart = requestedScope.LinesStart
		expected.LinesEnd = requestedScope.LinesEnd
	case command.StageRecent, command.StageSize:
	default:
		return resolvedScopeView{}, nil, false, nil
	}
	if !reflect.DeepEqual(expected, requestedScope) {
		return resolvedScopeView{}, nil, false, nil
	}

	if parent.inventory == nil {
		return resolvedScopeView{}, nil, false, nil
	}
	started := time.Now()
	var fileIDs []uint32
	snippetMatchLines := cloneSnippetLineOverlay(parent.snippetMatchLines)
	var eligible bool
	var err error
	if stage.Kind == command.StageNoIgnore {
		fileIDs, eligible, err = applyNoIgnoreScopeStageIDs(parent, requestedScope, requested.Config)
	} else if stage.Kind == command.StageRecent || stage.Kind == command.StageSize {
		fileIDs, eligible, err = applyMetadataScopeStageIDs(parent.inventory, parent.fileIDs, stage, requested.Config.WorkingDir)
	} else if stage.Kind == command.StageContains || stage.Kind == command.StageNotContains || stage.Kind == command.StageSnippet {
		retained, retainedOK := scopeViewMemoTakeContentStage(requestedKey, stage)
		var stageLines map[uint32][]int
		fileIDs, stageLines, eligible, err = applyContentScopeStageIDs(parent.inventory, parent.fileIDs, stage, requested.Config.WorkingDir, retained, retainedOK)
		if stage.Kind == command.StageSnippet && eligible && err == nil {
			snippetMatchLines = stageLines
		}
	} else if isRetainedGitStage(stage.Kind) {
		if !parent.view.GitContext.Enabled {
			fileIDs, eligible = nil, true
		} else {
			fileIDs, eligible, err = applyGitScopeStageIDs(parent.inventory, parent.fileIDs, stage, parent.view.GitContext)
		}
	} else {
		parent.inventory.mu.RLock()
		fileIDs, eligible, err = discovery.ApplyPathOnlyStageIDs(requestedScope, stage, parent.inventory.entries, parent.fileIDs)
		parent.inventory.mu.RUnlock()
	}
	if !eligible {
		return resolvedScopeView{}, nil, false, nil
	}
	view := parent.view
	view.Invocation = requested.Config
	view.Render = renderCfg
	view.Scopes = append([]command.ExecutionScope(nil), requested.Scopes...)
	view.ScopeIndex = last
	view.Scope = requestedScope
	view.snippetMatchLines = snippetMatchLines
	view.Entries = nil
	view.Discovered.Scope = requestedScope
	view.Discovered.Entries = nil
	view.Discovered.Diagnostics = append([]discovery.Diagnostic(nil), parent.view.Discovered.Diagnostics...)
	if isRetainedGitStage(stage.Kind) && !parent.view.GitContext.Enabled {
		view.Discovered.Diagnostics = append(view.Discovered.Diagnostics, discovery.GitSelectionUnavailableDiagnostic(last))
	} else {
		view.Discovered.Diagnostics = append(view.Discovered.Diagnostics,
			discovery.PathOnlyStageDiagnostics(stage, last, platform.ActivePalette(), len(fileIDs) == 0)...)
	}
	view.Discovered.Notices = append([]string(nil), parent.view.Discovered.Notices...)
	view.Duration += time.Since(started)
	return view, fileIDs, true, err
}

func isRetainedGitStage(kind command.StageKind) bool {
	switch kind {
	case command.StageChanged, command.StageStaged, command.StageUnstaged, command.StageUntracked,
		command.StageChangedDiff, command.StageStagedDiff, command.StageUnstagedDiff:
		return true
	default:
		return false
	}
}

// applyGitScopeStageIDs projects one cached porcelain observation over the
// current retained membership. Sibling Git filters and undo share the same
// inventory-wide observation; the *-diff variants change only output shape.
func applyGitScopeStageIDs(inventory *scopeViewInventory, ids []uint32, stage command.Stage, gitCtx git.Context) ([]uint32, bool, error) {
	if inventory == nil || !isRetainedGitStage(stage.Kind) {
		return nil, false, nil
	}
	if !gitCtx.Enabled {
		return nil, false, nil
	}

	statuses, _, err := inventory.cachedGitStatus(nil, func(entries []discovery.Entry) (map[string]string, error) {
		return git.StatusMapForPathspecs(gitCtx, discovery.GitStatusPathspecsForEntries(gitCtx, entries))
	})
	if err != nil {
		return nil, true, err
	}
	inventory.mu.RLock()
	defer inventory.mu.RUnlock()
	out, ok := discovery.ApplyGitStatusStageIDs(gitCtx, stage, inventory.entries, ids, statuses)
	if !ok {
		return nil, false, nil
	}
	return out, true, nil
}

// applyNoIgnoreScopeStageIDs expands one retained visible generation in place.
// Existing file IDs and metadata stay stable; only newly admitted ignored
// paths are appended and receive a primary Lstat. The exact derived argv state
// is memoized by the caller, so undo and re-entry perform neither operation.
func applyNoIgnoreScopeStageIDs(parent scopeViewMemoEntry, scope command.ExecutionScope, cfg command.Invocation) ([]uint32, bool, error) {
	finishBench := platform.InternalBenchSpan("ui.scope_view.apply_no_ignore_delta",
		"entries", platform.InternalBenchInt(len(parent.fileIDs)),
	)
	inventory := parent.inventory
	if inventory == nil {
		finishBench("eligible", "false", "err", "false")
		return nil, false, nil
	}
	inventory.metadataMu.Lock()
	defer inventory.metadataMu.Unlock()
	inventory.mu.RLock()
	if !inventory.metadataSealed {
		inventory.mu.RUnlock()
		finishBench("eligible", "false", "err", "false", "reason", "metadata-unsealed")
		return nil, false, nil
	}
	retained := make([]discovery.Entry, 0, len(parent.fileIDs))
	for _, id := range parent.fileIDs {
		if uint64(id) >= uint64(len(inventory.entries)) || !inventory.metadataKnown[id] {
			inventory.mu.RUnlock()
			finishBench("eligible", "false", "err", "false", "reason", "invalid-parent")
			return nil, false, nil
		}
		retained = append(retained, inventory.entries[id])
	}
	inventory.mu.RUnlock()

	expanded, err := discovery.ExpandEntriesUnderNoIgnore(cfg, parent.view.GitContext, scope, retained)
	if err != nil {
		finishBench("eligible", "true", "err", platform.InternalBenchError(err))
		return nil, true, err
	}

	inventory.mu.RLock()
	idsByPath := make(map[string]uint32, len(inventory.entries))
	for i := range inventory.entries {
		idsByPath[inventory.entries[i].RelPath] = uint32(i)
	}
	inventory.mu.RUnlock()
	newPaths := make([]string, 0, len(expanded)-len(retained))
	for _, entry := range expanded {
		if _, ok := idsByPath[entry.RelPath]; !ok {
			newPaths = append(newPaths, entry.RelPath)
		}
	}
	capture := search.StartTextSizeCapture(cfg.WorkingDir, newPaths)
	<-capture.Done()
	newMetadata := capture.MetadataSnapshot()

	inventory.mu.Lock()
	out := make([]uint32, 0, len(expanded))
	for _, entry := range expanded {
		if id, ok := idsByPath[entry.RelPath]; ok {
			out = append(out, id)
			continue
		}
		record, ok := newMetadata[entry.RelPath]
		if !ok {
			inventory.mu.Unlock()
			finishBench("eligible", "true", "err", "missing-metadata")
			return nil, true, os.ErrInvalid
		}
		id := uint32(len(inventory.entries))
		idsByPath[entry.RelPath] = id
		inventory.initialSizeKnown = append(inventory.initialSizeKnown, entry.SizeKnown)
		inventory.initialModTimeKnown = append(inventory.initialModTimeKnown, !entry.ModTime.IsZero())
		if record.State == search.FileMetadataReady {
			entry.SizeBytes = record.SizeBytes
			entry.SizeKnown = true
			entry.ModTime = record.ModTime
		}
		entry.SnippetMatchLines = append([]int(nil), entry.SnippetMatchLines...)
		inventory.entries = append(inventory.entries, entry)
		inventory.metadata = append(inventory.metadata, record)
		inventory.metadataKnown = append(inventory.metadataKnown, true)
		out = append(out, id)
	}
	inventory.metadataSealed = allMetadataKnown(inventory.metadataKnown)
	inventory.mu.Unlock()
	if len(newPaths) > 0 {
		// The porcelain observation is inventory-wide. An authorized universe
		// expansion invalidates it so the next Git stage observes the complete
		// new generation exactly once.
		inventory.observationMu.Lock()
		inventory.gitStatusKnown = false
		inventory.gitStatus = nil
		inventory.observationMu.Unlock()
	}
	finishBench(
		"eligible", "true",
		"entries", platform.InternalBenchInt(len(out)),
		"added", platform.InternalBenchInt(len(newPaths)),
		"err", "false",
	)
	return out, true, nil
}

// applyContentScopeStageIDs projects an exact live-picker survivor set when
// available; otherwise it runs the required PCRE2 predicate against the
// current retained membership. Both routes reuse the preceding inventory and
// ordered IDs, avoiding target discovery, text classification, and metadata
// work during the handoff to the next filter or output picker.
func applyContentScopeStageIDs(inventory *scopeViewInventory, ids []uint32, stage command.Stage, workingDir string, retained retainedContentStageResult, retainedOK bool) ([]uint32, map[uint32][]int, bool, error) {
	finishBench := platform.InternalBenchSpan("ui.scope_view.apply_content_delta",
		"stage", string(stage.Kind),
		"entries", platform.InternalBenchInt(len(ids)),
	)
	if inventory == nil || (stage.Kind != command.StageContains && stage.Kind != command.StageNotContains && stage.Kind != command.StageSnippet) || len(stage.Values) != 1 {
		finishBench("eligible", "false", "err", "false")
		return nil, nil, false, nil
	}
	if retainedOK {
		inventory.mu.RLock()
		out := make([]uint32, 0, len(retained.survivors))
		var snippetLines map[uint32][]int
		for _, id := range ids {
			if uint64(id) >= uint64(len(inventory.entries)) {
				inventory.mu.RUnlock()
				finishBench("eligible", "false", "retained", "true", "err", "false")
				return nil, nil, false, nil
			}
			entry := inventory.entries[id]
			absPath := entry.AbsPath
			if absPath == "" {
				absPath = filepath.Join(workingDir, filepath.FromSlash(entry.RelPath))
			}
			if _, ok := retained.survivors[filepath.Clean(absPath)]; ok {
				out = append(out, id)
				if stage.Kind == command.StageSnippet {
					lines := retained.snippetLines[filepath.Clean(absPath)]
					if len(lines) == 0 {
						inventory.mu.RUnlock()
						finishBench("eligible", "false", "retained", "true", "err", "missing-snippet-lines")
						return nil, nil, false, nil
					}
					if snippetLines == nil {
						snippetLines = make(map[uint32][]int)
					}
					snippetLines[id] = append([]int(nil), lines...)
				}
			}
		}
		inventory.mu.RUnlock()
		if len(out) != len(retained.survivors) {
			finishBench("eligible", "false", "retained", "true", "err", "false", "matches", platform.InternalBenchInt(len(out)))
			return nil, nil, false, nil
		}
		finishBench(
			"eligible", "true",
			"retained", "true",
			"matches", platform.InternalBenchInt(len(out)),
			"err", "false",
		)
		return out, snippetLines, true, nil
	}
	inventory.mu.RLock()
	entries := make([]discovery.Entry, 0, len(ids))
	idsByPath := make(map[string]uint32, len(ids))
	for _, id := range ids {
		if uint64(id) >= uint64(len(inventory.entries)) {
			inventory.mu.RUnlock()
			finishBench("eligible", "false", "err", "false")
			return nil, nil, false, nil
		}
		entry := inventory.entries[id]
		entries = append(entries, entry)
		idsByPath[entry.RelPath] = id
	}
	inventory.mu.RUnlock()

	entries = discovery.EnsureEntryAbsPaths(entries, workingDir)
	var (
		matched []discovery.Entry
		err     error
	)
	switch stage.Kind {
	case command.StageContains:
		matched, err = discovery.FilterEntriesByContent(entries, stage.Values[0])
	case command.StageNotContains:
		matched, err = discovery.FilterEntriesByNotContent(entries, stage.Values[0])
	case command.StageSnippet:
		matched, err = discovery.FilterEntriesBySnippetContent(entries, stage.Values[0])
	}
	if err != nil {
		finishBench("eligible", "true", "err", platform.InternalBenchError(err))
		return nil, nil, true, err
	}
	out := make([]uint32, 0, len(matched))
	var snippetLines map[uint32][]int
	for _, entry := range matched {
		id, ok := idsByPath[entry.RelPath]
		if !ok {
			finishBench("eligible", "false", "err", "false")
			return nil, nil, false, nil
		}
		out = append(out, id)
		if stage.Kind == command.StageSnippet {
			if snippetLines == nil {
				snippetLines = make(map[uint32][]int)
			}
			snippetLines[id] = append([]int(nil), entry.SnippetMatchLines...)
		}
	}
	finishBench(
		"eligible", "true",
		"retained", "false",
		"matches", platform.InternalBenchInt(len(out)),
		"err", "false",
	)
	return out, snippetLines, true, nil
}

// applyMetadataScopeStageIDs applies --size/--recent to a retained ordered ID
// set. Metadata already captured by the target picker or an earlier checkpoint
// makes the discovery helpers pure in-memory transforms. Any missing records
// are statted once by those helpers and published back to the shared inventory.
func applyMetadataScopeStageIDs(inventory *scopeViewInventory, ids []uint32, stage command.Stage, workingDir string) ([]uint32, bool, error) {
	if inventory == nil || (stage.Kind != command.StageRecent && stage.Kind != command.StageSize) {
		return nil, false, nil
	}
	inventory.metadataMu.Lock()
	defer inventory.metadataMu.Unlock()
	inventory.mu.RLock()
	entries := make([]discovery.Entry, 0, len(ids))
	idsByPath := make(map[string]uint32, len(ids))
	for _, id := range ids {
		if uint64(id) >= uint64(len(inventory.entries)) {
			inventory.mu.RUnlock()
			return nil, false, nil
		}
		entry := inventory.entries[id]
		if inventory.metadataSealed {
			if !inventory.metadataKnown[id] {
				inventory.mu.RUnlock()
				return nil, true, os.ErrInvalid
			}
			record := inventory.metadata[id]
			if record.State != search.FileMetadataReady {
				inventory.mu.RUnlock()
				if record.Error != "" {
					return nil, true, &os.PathError{Op: "lstat", Path: entry.AbsPath, Err: errors.New(record.Error)}
				}
				return nil, true, &os.PathError{Op: "lstat", Path: entry.AbsPath, Err: os.ErrNotExist}
			}
			entry.SizeBytes = record.SizeBytes
			entry.SizeKnown = true
			entry.ModTime = record.ModTime
		}
		entries = append(entries, entry)
		idsByPath[entry.RelPath] = id
	}
	inventory.mu.RUnlock()

	var (
		out []discovery.Entry
		err error
	)
	switch stage.Kind {
	case command.StageRecent:
		entries, err = discovery.EnsureEntryModTimes(entries, workingDir)
		if err != nil {
			return nil, true, err
		}
		out, err = discovery.ApplyRecentStage(entries, workingDir, stage.Limit)
	case command.StageSize:
		entries, err = discovery.EnsureEntrySizes(entries, workingDir)
		if err != nil {
			return nil, true, err
		}
		out, err = discovery.ApplySizeStage(entries, workingDir, stage.Nums)
	}
	if err != nil {
		return nil, true, err
	}

	inventory.mu.Lock()
	for _, entry := range entries {
		id, ok := idsByPath[entry.RelPath]
		if !ok || uint64(id) >= uint64(len(inventory.entries)) {
			continue
		}
		if entry.SizeKnown {
			inventory.entries[id].SizeBytes = entry.SizeBytes
			inventory.entries[id].SizeKnown = true
		}
		if !entry.ModTime.IsZero() {
			inventory.entries[id].ModTime = entry.ModTime
		}
	}
	inventory.mu.Unlock()

	outIDs := make([]uint32, 0, len(out))
	for _, entry := range out {
		id, ok := idsByPath[entry.RelPath]
		if !ok {
			return nil, false, nil
		}
		outIDs = append(outIDs, id)
	}
	return outIDs, true, nil
}

// ScopeViewForDiscoveryArgs adapts resolvedCurrentScopeViewForArgs into
// the (ScopeView, bool) shape discovery.SetScopeViewResolver expects.
// Registered from Main() so the resolver's fzf checkpoint-preview path
// can drive args -> entries without root needing to import directly.
func ScopeViewForDiscoveryArgs(args []string) (discovery.ScopeView, bool) {
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		return discovery.ScopeView{}, false
	}
	return discovery.ScopeView{
		WorkingDir: view.Invocation.WorkingDir,
		GitContext: view.GitContext,
		Entries:    view.Entries,
		Targets:    view.Scope.Targets,
	}, true
}

func resolvedCurrentScopeView(invocation command.Resolved, renderCfg RenderConfig) (resolvedScopeView, error) {
	if len(invocation.Scopes) == 0 {
		return resolvedScopeView{}, nil
	}

	finishBench := platform.InternalBenchSpan("ui.resolved_scope_view",
		"scopes", platform.InternalBenchInt(len(invocation.Scopes)),
	)
	invocationCfg := invocation.Config
	resolvedScopes := append([]command.ExecutionScope(nil), invocation.Scopes...)
	finishGitBench := platform.InternalBenchSpan("ui.resolved_scope_view.git_detect")
	gitCtx := git.Detect(invocationCfg.WorkingDir)
	finishGitBench("enabled", platform.InternalBenchBool(gitCtx.Enabled))
	scopeIndex := len(resolvedScopes) - 1
	currentScope := resolvedScopes[scopeIndex]
	finishEvalBench := platform.InternalBenchSpan("ui.resolved_scope_view.evaluate_scope",
		"scope_index", platform.InternalBenchInt(scopeIndex),
	)
	evalStarted := time.Now()
	discovered, err := discovery.EvaluateScope(invocationCfg, gitCtx, scopeIndex, currentScope, io.Discard, platform.ActivePalette())
	evalDuration := time.Since(evalStarted)
	finishEvalBench(
		"err", platform.InternalBenchError(err),
		"entries", platform.InternalBenchInt(len(discovered.Entries)),
	)
	if err != nil {
		finishBench("err", platform.InternalBenchError(err))
		return resolvedScopeView{}, err
	}
	entries := discovered.Entries

	finishBench(
		"err", "false",
		"entries", platform.InternalBenchInt(len(entries)),
	)
	return resolvedScopeView{
		Invocation: invocationCfg,
		Render:     renderCfg,
		GitContext: gitCtx,
		Scopes:     resolvedScopes,
		ScopeIndex: scopeIndex,
		Scope:      currentScope,
		Entries:    entries,
		Discovered: discovered,
		Duration:   evalDuration,
	}, nil
}

func startupResolvedCurrentScopeViewForArgs(args []string) (resolvedScopeView, bool, error) {
	if startupHasUnresolvedScope(args) {
		return resolvedScopeView{}, false, nil
	}
	if _, action, ok := detectStartupTrailingAction(args); ok && action != startupTrailingActionNone {
		return resolvedScopeView{}, false, nil
	}

	// Route through the memoized deriver: this is the target-picker-phase
	// derivation, and storing it here is what lets the modifier menu's
	// identical-args lookup hit — the exact hop the cross-picker plan
	// measured at ~144 ms of redundant rg walks.
	view, err := resolvedCurrentScopeViewForArgs(args)
	if err != nil {
		return resolvedScopeView{}, false, err
	}
	if len(view.Scopes) == 0 {
		return resolvedScopeView{}, false, nil
	}
	return view, true, nil
}
