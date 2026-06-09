package command

// RenderFlags carries the boolean / mode toggles that canonical command
// rendering needs from the runtime emit config and the parsed Invocation.
// It detaches the canonical-render layer from emit.go's emitConfig (a
// root/runtime concern), so the render functions can stay command-local
// in the upcoming move of command_render.go into internal/command.
//
// Fields map 1:1 from root emitConfig + command.Parsed toggles:
//   - OutputMode: emitConfig.OutputMode
//   - Raw, NoBundle: emitConfig.Raw, .NoBundle
//   - Yes, NoTree, Preview: command.Parsed.Yes, .NoTree, .Preview
//
// Pass these through canonical-render call sites instead of emitConfig +
// trailing positional bools. The reviewer flagged the positional-bool
// signature as harder to read and easier to misorder.
type RenderFlags struct {
	OutputMode OutputMode
	Raw        bool
	NoBundle   bool
	Yes        bool
	NoTree     bool
	Preview    bool
}

// RenderFlagsFromParsed packs the canonical-render toggles out of a
// Parsed argv model. Pure POD-to-POD mapping; lives in command so the
// cli/ parser-adjacent layer can call it directly without dragging the
// root emit.go's emitConfig type.
func RenderFlagsFromParsed(cfg Parsed) RenderFlags {
	return RenderFlags{
		OutputMode: cfg.OutputMode,
		Raw:        cfg.Raw,
		NoBundle:   cfg.NoBundle,
		Yes:        cfg.Yes,
		NoTree:     cfg.NoTree,
		Preview:    cfg.Preview,
	}
}

// InvocationFromParsed packs the runtime-wide toggles out of a Parsed
// argv model. Internal is derived from Parsed.IsInternalKind() so
// internal preview/reload commands suppress prompts (parity with root
// invocationConfigFromParsedCommand, which uses the equivalent root
// predicate internalCommandConfig.isInternalKind).
func InvocationFromParsed(cfg Parsed) Invocation {
	return Invocation{
		Version:      cfg.Version,
		Platform:     cfg.Platform,
		WorkingDir:   cfg.WorkingDir,
		Verbose:      cfg.Verbose,
		Quiet:        cfg.Quiet,
		Headless:     cfg.Headless,
		WithBinaries: cfg.WithBinaries,
		Internal:     cfg.IsInternalKind(),
	}
}

// ResolvedFromParsed pairs InvocationFromParsed with the canonical
// []ExecutionScope derived from the Parsed's Spec.
func ResolvedFromParsed(cfg Parsed) Resolved {
	return Resolved{
		Config: InvocationFromParsed(cfg),
		Scopes: ExecutionScopesFromSpec(cfg.Command),
	}
}
