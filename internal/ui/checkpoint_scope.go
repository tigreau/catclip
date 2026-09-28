package ui

import (
	"fmt"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
)

// scopeFromCheckpoint separates committed state from the one live operation.
// Stored stages explain ordering/projection; they are not work to replay.
func scopeFromCheckpoint(live command.ExecutionScope, checkpoint discovery.CheckpointData, content bool) (command.ExecutionScope, error) {
	if checkpoint.Scope == nil || len(checkpoint.Scope.Targets) == 0 {
		return command.ExecutionScope{}, fmt.Errorf("checkpoint is missing its retained scope")
	}
	for _, target := range checkpoint.Scope.Targets {
		if target == "" {
			return command.ExecutionScope{}, fmt.Errorf("checkpoint contains an empty target")
		}
		if err := discovery.ValidateTargetBoundary(target); err != nil {
			return command.ExecutionScope{}, err
		}
	}
	if len(live.Targets) != 1 || live.Targets[0] != "." || live.NoIgnore || live.Paths {
		return command.ExecutionScope{}, fmt.Errorf("checkpoint-scope preview does not accept positional targets or output overrides")
	}
	if !content {
		if len(live.Stages) != 0 {
			return command.ExecutionScope{}, fmt.Errorf("checkpoint-scope tree preview does not accept pending stages")
		}
		return *checkpoint.Scope, nil
	}
	if len(live.Stages) != 1 {
		return command.ExecutionScope{}, fmt.Errorf("checkpoint-scope content preview requires exactly one live content stage")
	}
	switch live.Stages[0].Kind {
	case command.StageContains, command.StageNotContains, command.StageSnippet:
	default:
		return command.ExecutionScope{}, fmt.Errorf("invalid live checkpoint content stage")
	}
	// Keep the parent target restriction for direct rg, but not its already
	// evaluated filters. Membership is enforced by checkpoint intersection.
	live.Targets = append([]string(nil), checkpoint.Scope.Targets...)
	return live, nil
}
