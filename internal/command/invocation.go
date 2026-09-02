package command

// Invocation is the parsed top-level runtime configuration. The CLI parser
// fills it once per `catclip` invocation; everything after parse / resolve
// threads it through unchanged.
//
// WorkingDir is the runtime cwd snapshot; EmissionPolicy is the invocation-wide
// final-output decision; Internal flips true when the process is a short-lived
// --internal-* helper spawned by fzf (suppresses prompts).
type Invocation struct {
	Version        string
	Platform       string
	WorkingDir     string
	Verbose        bool
	Quiet          bool
	Headless       bool
	WithBinaries   bool
	PayloadKind    PayloadKind
	EmissionPolicy EmissionPolicy
	Internal       bool
}

// Resolved is the parsed Invocation paired with the resolved set of
// ExecutionScopes. The discoveredInvocation (which carries discovery
// results) is the next stage of the pipeline and lives root/discovery-side
// because it drags fileEntry.
type Resolved struct {
	Config Invocation
	Scopes []ExecutionScope
}
