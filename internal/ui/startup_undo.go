package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tigreau/catclip/internal/cli"
	"github.com/tigreau/catclip/internal/discovery"
)

type startupInteractiveFrameKind string

const (
	startupInteractiveFrameTarget     startupInteractiveFrameKind = "target"
	startupInteractiveFrameModifier   startupInteractiveFrameKind = "modifier"
	startupInteractiveFrameStage      startupInteractiveFrameKind = "stage"
	startupInteractiveFrameLinesStart startupInteractiveFrameKind = "lines-start"
	startupInteractiveFrameLinesEnd   startupInteractiveFrameKind = "lines-end"
	startupInteractiveFrameSink       startupInteractiveFrameKind = "sink"
)

type startupInteractiveFrame struct {
	Kind                       startupInteractiveFrameKind
	StartArgs                  []string
	PendingArgs                []string
	EscHint                    string
	TargetTokens               []string
	TargetPrompt               string
	ChoiceArgs                 []string
	AllowInteractiveCompletion bool
	LinesCheckpointPath        string
	LinesCleanup               func()
	LinesMax                   int
	LinesStart                 int
}

type startupInteractiveFrameResult struct {
	Args                 []string
	Pending              []string
	UsedFzf              bool
	NextFrame            *startupInteractiveFrame
	ScopePaths           []string
	PreparedOutput       *StartupPreparedOutputState
	ForceResolvedCommand bool
	SinkResolved         bool
}

func resolveStartupArgsWithUndo(resolver *discovery.Resolver, args []string) ([]string, []string, bool, error) {
	result, err := resolveStartupWithUndo(resolver, args, startupUndoOptions{})
	if err != nil {
		return nil, nil, result.UsedFzf, err
	}
	return result.Args, result.ScopePaths, result.UsedFzf, nil
}

func resolveStartupPickerResultWithUndo(resolver *discovery.Resolver, rawArgs []string) (StartupPickerResult, error) {
	result, err := resolveStartupWithUndo(resolver, rawArgs, startupUndoOptions{
		RawArgs:     rawArgs,
		IncludeSink: true,
	})
	if err != nil {
		return StartupPickerResult{UsedFzf: result.UsedFzf}, err
	}
	return StartupPickerResult{
		Args:                 result.Args,
		UsedFzf:              result.UsedFzf,
		PreparedOutput:       result.PreparedOutput,
		ForceResolvedCommand: result.ForceResolvedCommand,
	}, nil
}

type startupUndoOptions struct {
	RawArgs     []string
	IncludeSink bool
}

type startupUndoResult struct {
	Args                 []string
	ScopePaths           []string
	UsedFzf              bool
	PreparedOutput       *StartupPreparedOutputState
	ForceResolvedCommand bool
}

func resolveStartupWithUndo(resolver *discovery.Resolver, args []string, opts startupUndoOptions) (startupUndoResult, error) {
	// One process normally serves one invocation, but tests exercise many
	// sessions in-process. Bound retained scope states to this interactive run
	// so undo can reuse them without leaking filesystem views across sessions.
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	defer resolver.ReleaseRetainedTargetPreviewInventory()

	currentArgs := []string{}
	pendingArgs := cloneStringSlice(args)
	history := make([]startupInteractiveFrame, 0, 8)
	var queuedFrame *startupInteractiveFrame
	usedFzf := false
	sinkResolved := false
	var preparedOutput *StartupPreparedOutputState
	forceResolvedCommand := false

	for {
		var frame startupInteractiveFrame
		if queuedFrame != nil {
			frame = *queuedFrame
			queuedFrame = nil
		} else {
			next, doneArgs, done, err := nextStartupInteractiveFrame(resolver, currentArgs, pendingArgs)
			if err != nil {
				return startupUndoResult{}, err
			}
			if done {
				if opts.IncludeSink && !sinkResolved && usedFzf && len(doneArgs) > 0 {
					if !rawArgsSkipOutputSinkPicker(opts.RawArgs) {
						frame = startupInteractiveFrame{
							Kind:      startupInteractiveFrameSink,
							StartArgs: cloneStringSlice(doneArgs),
						}
					} else {
						// An explicit sink suppresses the picker, not the retained
						// handoff. Prepare the exact sealed discovery/plan now so final
						// execution cannot reconstruct the command and rediscover.
						ctx, prepareErr := buildStartupSinkPickerContext(doneArgs)
						if prepareErr != nil {
							return startupUndoResult{}, prepareErr
						}
						preparedOutput = &StartupPreparedOutputState{
							Git:       ctx.Git,
							Discovery: ctx.Discovery,
							Plan:      ctx.Plan,
						}
						sinkResolved = true
					}
				}
				if frame.Kind != startupInteractiveFrameSink {
					scopePaths, _ := startupFrameCurrentScopeSelections(doneArgs)
					cleanupStartupFrames(history)
					return startupUndoResult{
						Args:                 doneArgs,
						ScopePaths:           scopePaths,
						UsedFzf:              usedFzf,
						PreparedOutput:       preparedOutput,
						ForceResolvedCommand: forceResolvedCommand,
					}, nil
				}
			} else {
				frame = next
			}
		}

		frame.EscHint = startupEscHintForDepth(len(history) + 1)
		result, err := runStartupInteractiveFrame(resolver, frame, opts.RawArgs)
		if err != nil {
			if err == discovery.ErrSelectionCancelled {
				preparedOutput = nil
				forceResolvedCommand = false
				sinkResolved = false
				if len(history) == 0 {
					return startupUndoResult{UsedFzf: usedFzf}, discovery.ErrSelectionCancelled
				}
				previous := history[len(history)-1]
				history = history[:len(history)-1]
				currentArgs = cloneStringSlice(previous.StartArgs)
				pendingArgs = cloneStringSlice(previous.PendingArgs)
				queuedFrame = &previous
				continue
			}
			cleanupStartupFrames(history)
			return startupUndoResult{}, err
		}
		if frame.Kind == startupInteractiveFrameTarget {
			sealed := false
			if entries, metadata, ok := resolver.CommittedTargetSelection(); ok {
				targetInventoryPath, _ := resolver.CommittedTargetPreviewInventoryPath()
				sealed = scopeViewMemoAdoptTargetSelection(result.Args, resolver.GitCtx, entries, metadata, targetInventoryPath)
			}
			if !sealed {
				if sealErr := scopeViewMemoSealEvaluatedTarget(result.Args); sealErr != nil {
					cleanupStartupFrames(history)
					return startupUndoResult{}, fmt.Errorf("internal error: target selection could not be sealed: %w", sealErr)
				}
			}
		}

		if result.UsedFzf {
			history = append(history, frame)
			usedFzf = true
		}
		if result.SinkResolved {
			sinkResolved = true
			preparedOutput = result.PreparedOutput
			forceResolvedCommand = forceResolvedCommand || result.ForceResolvedCommand
		}
		currentArgs = cloneStringSlice(result.Args)
		pendingArgs = cloneStringSlice(result.Pending)
		if result.NextFrame != nil {
			queuedFrame = result.NextFrame
		}
	}
}

func nextStartupInteractiveFrame(resolver *discovery.Resolver, currentArgs, pendingArgs []string) (startupInteractiveFrame, []string, bool, error) {
	args := cloneStringSlice(currentArgs)
	pending := cloneStringSlice(pendingArgs)

	for {
		if len(pending) == 0 {
			if !startupFrameCurrentScopeHasInput(args) {
				return startupInteractiveFrame{
					Kind:         startupInteractiveFrameTarget,
					StartArgs:    cloneStringSlice(args),
					PendingArgs:  nil,
					TargetPrompt: startupFrameTargetPrompt(args),
				}, nil, false, nil
			}
			return startupInteractiveFrame{}, args, true, nil
		}

		arg := pending[0]
		if cli.IsUnsupportedIncludeOption(arg) {
			return startupInteractiveFrame{}, nil, false, cli.IncludeUnsupportedError()
		}
		if !startupFrameCurrentScopeHasInput(args) && startupLeadingModifierNeedsInitialScope(arg) {
			return startupInteractiveFrame{
				Kind:         startupInteractiveFrameTarget,
				StartArgs:    cloneStringSlice(args),
				PendingArgs:  cloneStringSlice(pending),
				TargetPrompt: startupFrameTargetPrompt(args),
			}, nil, false, nil
		}

		switch arg {
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
			args = append(args, arg)
			pending = pending[1:]
			continue
		case "--then":
			args = append(args, "--then")
			pending = pending[1:]
			continue
		case "--":
			return startupInteractiveFrame{
				Kind:        startupInteractiveFrameModifier,
				StartArgs:   cloneStringSlice(args),
				PendingArgs: cloneStringSlice(pending[1:]),
			}, nil, false, nil
		case "--no-ignore", "--only", "--exclude", "--contains", "--not-contains", "--snippet", "--recent", "--size", "--depth", "--paths", "--lines",
			"--changed", "--staged", "--unstaged", "--untracked", "--changed-diff", "--staged-diff", "--unstaged-diff":
			return startupInteractiveFrame{
				Kind:                       startupInteractiveFrameStage,
				StartArgs:                  cloneStringSlice(args),
				PendingArgs:                cloneStringSlice(pending[1:]),
				ChoiceArgs:                 []string{arg},
				AllowInteractiveCompletion: false,
			}, nil, false, nil
		default:
			if err := cli.EqualsFormRejectionError(arg); err != nil {
				return startupInteractiveFrame{}, nil, false, err
			}
			switch {
			case strings.HasPrefix(arg, "--"):
				return startupInteractiveFrame{}, nil, false, cli.UnknownOptionError(arg)
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return startupInteractiveFrame{}, nil, false, cli.UnknownOptionError(arg)
			}
			if startupFrameCurrentScopeHasModifier(args) {
				return startupInteractiveFrame{}, nil, false, cli.PositionalAfterModifierError()
			}
			return startupInteractiveFrame{
				Kind:         startupInteractiveFrameTarget,
				StartArgs:    cloneStringSlice(args),
				PendingArgs:  cloneStringSlice(pending[1:]),
				TargetTokens: []string{arg},
				TargetPrompt: startupFrameTargetPrompt(args),
			}, nil, false, nil
		}
	}
}

func runStartupInteractiveFrame(resolver *discovery.Resolver, frame startupInteractiveFrame, rawArgs []string) (startupInteractiveFrameResult, error) {
	switch frame.Kind {
	case startupInteractiveFrameTarget:
		return runStartupTargetFrame(resolver, frame)
	case startupInteractiveFrameModifier:
		return runStartupModifierFrame(frame)
	case startupInteractiveFrameStage:
		return runStartupStageFrame(resolver, frame)
	case startupInteractiveFrameLinesStart:
		return runStartupLinesStartFrame(frame)
	case startupInteractiveFrameLinesEnd:
		return runStartupLinesEndFrame(frame)
	case startupInteractiveFrameSink:
		return runStartupSinkFrame(frame, rawArgs)
	default:
		return startupInteractiveFrameResult{}, discovery.ErrSelectionCancelled
	}
}

func cleanupStartupFrames(frames []startupInteractiveFrame) {
	for _, frame := range frames {
		if frame.LinesCleanup != nil {
			frame.LinesCleanup()
		}
	}
}

func runStartupSinkFrame(frame startupInteractiveFrame, rawArgs []string) (startupInteractiveFrameResult, error) {
	ctx, err := buildStartupSinkPickerContext(frame.StartArgs)
	if err != nil {
		return startupInteractiveFrameResult{}, err
	}
	prepared := &StartupPreparedOutputState{Git: ctx.Git, Discovery: ctx.Discovery, Plan: ctx.Plan}
	// Resolve this frame without fzf; normal execution owns empty-result
	// diagnostics, and a sink menu cannot offer anything useful here.
	if ctx.Plan.IsEmpty() {
		return startupInteractiveFrameResult{
			Args:           cloneStringSlice(frame.StartArgs),
			PreparedOutput: prepared,
			SinkResolved:   true,
		}, nil
	}
	measurement := measureOutputForSinkMenu(ctx.Plan, ctx.Emit)
	sinkArgs, usedFzf, err := pickOutputSinkWithEscHint(ctx, measurement, frame.EscHint)
	if err != nil {
		return startupInteractiveFrameResult{UsedFzf: usedFzf}, err
	}
	return startupInteractiveFrameResult{
		Args:                 append(cloneStringSlice(frame.StartArgs), sinkArgs...),
		UsedFzf:              usedFzf,
		PreparedOutput:       prepared,
		ForceResolvedCommand: argsContain(sinkArgs, "--headless") && !rawArgsRequestQuiet(rawArgs),
		SinkResolved:         true,
	}, nil
}

func runStartupLinesStartFrame(frame startupInteractiveFrame) (startupInteractiveFrameResult, error) {
	start, err := chooseStartupStartLineWithEscHint(frame.LinesCheckpointPath, frame.LinesMax, frame.EscHint)
	if err != nil {
		if frame.LinesCleanup != nil {
			frame.LinesCleanup()
		}
		return startupInteractiveFrameResult{UsedFzf: true}, err
	}
	next := frame
	next.Kind = startupInteractiveFrameLinesEnd
	next.LinesStart = start
	return startupInteractiveFrameResult{
		Args:      cloneStringSlice(frame.StartArgs),
		Pending:   cloneStringSlice(frame.PendingArgs),
		UsedFzf:   true,
		NextFrame: &next,
	}, nil
}

func runStartupLinesEndFrame(frame startupInteractiveFrame) (startupInteractiveFrameResult, error) {
	end, isOpenEnd, err := chooseStartupEndLineWithEscHint(frame.LinesCheckpointPath, frame.LinesStart, frame.LinesMax, frame.EscHint)
	if err != nil {
		if err != discovery.ErrSelectionCancelled && frame.LinesCleanup != nil {
			frame.LinesCleanup()
		}
		return startupInteractiveFrameResult{UsedFzf: true}, err
	}
	args := append(cloneStringSlice(frame.StartArgs), "--lines", strconv.Itoa(frame.LinesStart))
	if !isOpenEnd {
		args = append(args, strconv.Itoa(end))
	}
	return startupInteractiveFrameResult{
		Args:    args,
		Pending: cloneStringSlice(frame.PendingArgs),
		UsedFzf: true,
	}, nil
}

func runStartupTargetFrame(resolver *discovery.Resolver, frame startupInteractiveFrame) (startupInteractiveFrameResult, error) {
	currentScopeTargets, currentScopeExplicitTargets := startupFrameCurrentScopeSelections(frame.StartArgs)
	oldEscHint := resolver.StartupEscHint
	oldNoIgnore := resolver.NoIgnore
	resolver.StartupEscHint = frame.EscHint
	resolver.NoIgnore = startupTargetFrameUsesNoIgnore(frame)
	defer func() {
		resolver.StartupEscHint = oldEscHint
		resolver.NoIgnore = oldNoIgnore
	}()
	resolvedArgs, resolvedTargets, _, usedFzf, err := resolveStartupScopeInputsWithPrompt(
		resolver,
		frame.TargetTokens,
		nil,
		currentScopeTargets,
		currentScopeExplicitTargets,
		frame.TargetPrompt,
	)
	if err != nil {
		return startupInteractiveFrameResult{UsedFzf: usedFzf}, err
	}
	finalArgs := append(cloneStringSlice(frame.StartArgs), resolvedArgs...)
	resolver.FinalizeTargetSelection(append(currentScopeTargets, resolvedTargets...))
	return startupInteractiveFrameResult{
		Args:       finalArgs,
		Pending:    cloneStringSlice(frame.PendingArgs),
		UsedFzf:    usedFzf,
		ScopePaths: append(currentScopeTargets, resolvedTargets...),
	}, nil
}

func startupTargetFrameUsesNoIgnore(frame startupInteractiveFrame) bool {
	args := make([]string, 0, len(frame.StartArgs)+len(frame.TargetTokens)+len(frame.PendingArgs))
	args = append(args, frame.StartArgs...)
	targetAt := len(args)
	args = append(args, frame.TargetTokens...)
	args = append(args, frame.PendingArgs...)
	if len(args) == 0 {
		return false
	}
	if targetAt >= len(args) {
		targetAt = len(args) - 1
	}
	return startupScopeContainsNoIgnore(args, targetAt)
}

func runStartupModifierFrame(frame startupInteractiveFrame) (startupInteractiveFrameResult, error) {
	choice, err := chooseStartupModifierWithProgress(
		frame.StartArgs,
		frame.EscHint,
		pendingFilterSlotCount(frame.PendingArgs),
	)
	if err != nil {
		return startupInteractiveFrameResult{UsedFzf: true}, err
	}
	if err := startupValidateModifierChoice(frame.StartArgs, choice); err != nil {
		return startupInteractiveFrameResult{UsedFzf: true}, err
	}

	finalArgs := trimTrailingModifierPlaceholders(cloneStringSlice(frame.StartArgs))
	switch choice.Mode {
	case startupModifierModeFinish:
		return startupInteractiveFrameResult{
			Args:    finalArgs,
			Pending: nil,
			UsedFzf: true,
		}, nil
	case startupModifierModeThen:
		return startupInteractiveFrameResult{
			Args:    append(finalArgs, "--then"),
			Pending: cloneStringSlice(frame.PendingArgs),
			UsedFzf: true,
		}, nil
	case startupModifierModeFlags:
		return startupInteractiveFrameResult{
			Args:    append(finalArgs, choice.Args...),
			Pending: cloneStringSlice(frame.PendingArgs),
			UsedFzf: true,
		}, nil
	default:
		next := startupInteractiveFrame{
			Kind:                       startupInteractiveFrameStage,
			StartArgs:                  finalArgs,
			PendingArgs:                cloneStringSlice(frame.PendingArgs),
			ChoiceArgs:                 cloneStringSlice(choice.Args),
			AllowInteractiveCompletion: true,
		}
		return startupInteractiveFrameResult{
			Args:      finalArgs,
			Pending:   cloneStringSlice(frame.PendingArgs),
			UsedFzf:   true,
			NextFrame: &next,
		}, nil
	}
}

func runStartupStageFrame(resolver *discovery.Resolver, frame startupInteractiveFrame) (startupInteractiveFrameResult, error) {
	if startupStageFrameShouldUseLinesFrames(frame) {
		session, directArgs, err := prepareStartupLinesPickerSession(frame.StartArgs)
		if err != nil {
			return startupInteractiveFrameResult{}, err
		}
		if directArgs != nil {
			return startupInteractiveFrameResult{
				Args:    directArgs,
				Pending: cloneStringSlice(frame.PendingArgs),
			}, nil
		}
		next := startupInteractiveFrame{
			Kind:                startupInteractiveFrameLinesStart,
			StartArgs:           cloneStringSlice(frame.StartArgs),
			PendingArgs:         cloneStringSlice(frame.PendingArgs),
			LinesCheckpointPath: session.CheckpointPath,
			LinesCleanup:        session.Cleanup,
			LinesMax:            session.MaxLines,
		}
		return startupInteractiveFrameResult{
			Args:      cloneStringSlice(frame.StartArgs),
			Pending:   cloneStringSlice(frame.PendingArgs),
			NextFrame: &next,
		}, nil
	}

	currentScopeTargets, currentScopeExplicitTargets := startupFrameCurrentScopeSelections(frame.StartArgs)
	oldEscHint := resolver.StartupEscHint
	resolver.StartupEscHint = frame.EscHint
	defer func() {
		resolver.StartupEscHint = oldEscHint
	}()
	argsAfterStage, newScopeTargets, usedFzf, consumed, err := resolveStartupModifierStageWithEscHint(
		resolver,
		frame.StartArgs,
		currentScopeTargets,
		currentScopeExplicitTargets,
		frame.ChoiceArgs,
		frame.PendingArgs,
		frame.AllowInteractiveCompletion,
		frame.EscHint,
	)
	if err != nil {
		return startupInteractiveFrameResult{UsedFzf: usedFzf}, err
	}
	if consumed > len(frame.PendingArgs) {
		consumed = len(frame.PendingArgs)
	}
	return startupInteractiveFrameResult{
		Args:       argsAfterStage,
		Pending:    cloneStringSlice(frame.PendingArgs[consumed:]),
		UsedFzf:    usedFzf,
		ScopePaths: newScopeTargets,
	}, nil
}

func startupStageFrameShouldUseLinesFrames(frame startupInteractiveFrame) bool {
	if !frame.AllowInteractiveCompletion || len(frame.ChoiceArgs) == 0 || frame.ChoiceArgs[0] != "--lines" {
		return false
	}
	return len(frame.PendingArgs) == 0 || startupRemainingIsBarePlaceholderChain(frame.PendingArgs)
}

func startupFrameCurrentScopeHasInput(args []string) bool {
	selected, _ := startupFrameCurrentScopeSelections(args)
	return len(selected) > 0
}

func startupFrameCurrentScopeSelections(args []string) ([]string, []string) {
	selected := make([]string, 0, len(args))
	explicit := make([]string, 0, len(args))
	inModifierMode := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--then":
			selected = selected[:0]
			explicit = explicit[:0]
			inModifierMode = false
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
			continue
		case "--no-ignore":
			inModifierMode = true
		case "--only", "--exclude":
			inModifierMode = true
			_, next := cli.ConsumeModifierValues(args, i+1)
			i = next - 1
		case "--contains", "--not-contains", "--snippet", "--depth":
			inModifierMode = true
			if i+1 < len(args) {
				i++
			}
		case "--recent":
			inModifierMode = true
			if i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]) {
				if _, err := cli.ParseRecentLimitToken(args[i+1]); err == nil {
					i++
				}
			}
		case "--size":
			inModifierMode = true
			for consumed := 0; consumed < 2 && i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]); consumed++ {
				if _, err := cli.ParseSizeBoundToken(args[i+1]); err != nil {
					break
				}
				i++
			}
		case "--lines":
			inModifierMode = true
			for i+1 < len(args) {
				if _, err := strconv.Atoi(args[i+1]); err != nil {
					break
				}
				i++
			}
		case "--paths", "--changed", "--staged", "--unstaged", "--untracked", "--changed-diff", "--staged-diff", "--unstaged-diff", "--":
			inModifierMode = true
		default:
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if !inModifierMode {
				selected = append(selected, arg)
				explicit = append(explicit, arg)
			}
		}
	}

	return cloneStringSlice(selected), cloneStringSlice(explicit)
}

func startupFrameCurrentScopeHasModifier(args []string) bool {
	inModifierMode := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--then":
			inModifierMode = false
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
			continue
		case "--no-ignore":
			inModifierMode = true
		case "--only", "--exclude":
			inModifierMode = true
			_, next := cli.ConsumeModifierValues(args, i+1)
			i = next - 1
		case "--contains", "--not-contains", "--snippet", "--depth":
			inModifierMode = true
			if i+1 < len(args) {
				i++
			}
		case "--recent":
			inModifierMode = true
			if i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]) {
				if _, err := cli.ParseRecentLimitToken(args[i+1]); err == nil {
					i++
				}
			}
		case "--size":
			inModifierMode = true
			for consumed := 0; consumed < 2 && i+1 < len(args) && !cli.IsModifierBoundaryToken(args[i+1]); consumed++ {
				if _, err := cli.ParseSizeBoundToken(args[i+1]); err != nil {
					break
				}
				i++
			}
		case "--lines":
			inModifierMode = true
			for i+1 < len(args) {
				if _, err := strconv.Atoi(args[i+1]); err != nil {
					break
				}
				i++
			}
		case "--paths", "--changed", "--staged", "--unstaged", "--untracked", "--changed-diff", "--staged-diff", "--unstaged-diff", "--":
			inModifierMode = true
		}
	}
	return inModifierMode
}

func startupFrameTargetPrompt(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		switch args[i] {
		case "--then":
			return "then> "
		case "-v", "--verbose", "-q", "--quiet", "-y", "--yes", "-p", "--print", "-r", "--raw", "-t", "--no-tree", "--no-bundle", "--preview", "--with-binaries":
			continue
		default:
			return "select> "
		}
	}
	return "select> "
}

func startupEscHintForDepth(depth int) string {
	if depth > 1 {
		return "undo"
	}
	return "exit"
}

func startupEscLabel(hint string) string {
	if hint == "undo" {
		return "[Esc] undo"
	}
	return "[Esc] exit"
}
