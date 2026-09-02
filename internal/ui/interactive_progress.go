package ui

import (
	"strings"

	"github.com/tigreau/catclip/internal/command"
)

type interactiveProgressExtras uint8

const (
	progressExtraVerbose interactiveProgressExtras = 1 << iota
	progressExtraQuiet
	progressExtraYes
	progressExtraNo
	progressExtraRaw
	progressExtraNoTree
	progressExtraWithBinaries
	progressExtraMetadata
)

func interactiveProgressExtrasFromParsed(cfg command.Parsed) interactiveProgressExtras {
	var extras interactiveProgressExtras
	if cfg.Verbose {
		extras |= progressExtraVerbose
	}
	if cfg.Quiet && !cfg.Headless {
		extras |= progressExtraQuiet
	}
	if cfg.EmissionPolicy == command.EmissionAlways {
		extras |= progressExtraYes
	} else if cfg.EmissionPolicy == command.EmissionNever {
		extras |= progressExtraNo
	}
	if cfg.Raw {
		extras |= progressExtraRaw
	}
	if cfg.NoTree {
		extras |= progressExtraNoTree
	}
	if cfg.WithBinaries {
		extras |= progressExtraWithBinaries
	}
	if cfg.PayloadKind == command.PayloadMetadata {
		extras |= progressExtraMetadata
	}
	return extras
}

// formatInteractiveFilterProgress renders the compact stage outline shown in
// filter and output pickers. It consumes already-parsed extras and scopes and
// performs no discovery, filesystem access, subprocess work, or command
// re-parsing.
func formatInteractiveFilterProgress(extras interactiveProgressExtras, scopes []command.ExecutionScope, filterSlots int) string {
	stageCount := 0
	for _, scope := range scopes {
		stageCount += len(scope.Stages)
	}
	var out strings.Builder
	// Reserve the complete short footer in the common case. The estimate is
	// intentionally generous so formatting normally performs one allocation.
	out.Grow(24 + stageCount*20 + len(scopes)*8 + filterSlots*3)
	out.WriteString("catclip")
	writeInteractiveProgressExtra(&out, extras, progressExtraVerbose, "--verbose")
	writeInteractiveProgressExtra(&out, extras, progressExtraQuiet, "--quiet")
	writeInteractiveProgressExtra(&out, extras, progressExtraYes, "--yes")
	writeInteractiveProgressExtra(&out, extras, progressExtraNo, "--no")
	writeInteractiveProgressExtra(&out, extras, progressExtraMetadata, "--metadata")
	writeInteractiveProgressExtra(&out, extras, progressExtraRaw, "--raw")
	writeInteractiveProgressExtra(&out, extras, progressExtraNoTree, "--no-tree")
	writeInteractiveProgressExtra(&out, extras, progressExtraWithBinaries, "--with-binaries")
	for scopeIndex, scope := range scopes {
		if scopeIndex > 0 {
			out.WriteString(" --then")
		}
		for _, stage := range scope.Stages {
			flag, ok := command.StageFlag(stage.Kind)
			if !ok {
				continue
			}
			out.WriteByte(' ')
			out.WriteString(flag)
		}
	}
	out.WriteString(" ▶")
	if filterSlots <= 0 {
		if extras&progressExtraNo != 0 {
			out.WriteString(" report")
		} else {
			out.WriteString(" output")
		}
		return out.String()
	}
	for range filterSlots {
		out.WriteString(" --")
	}
	return out.String()
}

func writeInteractiveProgressExtra(out *strings.Builder, extras, flag interactiveProgressExtras, name string) {
	if extras&flag == 0 {
		return
	}
	out.WriteByte(' ')
	out.WriteString(name)
}

func pendingFilterSlotCount(args []string) int {
	count := 1 // The current filter frame already consumed its opening --.
	for _, arg := range args {
		if arg == "--" {
			count++
		}
	}
	return count
}
