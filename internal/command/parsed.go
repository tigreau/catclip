package command

// Action identifies which top-level operation the parsed command line
// requested. ActionRun is the normal copy-content path; the others are
// help / version / hiss-management actions that short-circuit before
// discovery runs.
type Action string

const (
	ActionRun             Action = "run"
	ActionHelp            Action = "help"
	ActionHelpAll         Action = "help-all"
	ActionVersion         Action = "version"
	ActionCheckUpdate     Action = "check-update"
	ActionEditHiss        Action = "hiss"
	ActionResetHiss       Action = "hiss-reset"
	ActionListIgnoreRules Action = "list-ignore-rules"
)

// OutputMode is the sink the final emitter writes to: the system clipboard
// (default) or stdout (when --print/-p or the like is in effect).
type OutputMode string

const (
	OutputModeClipboard OutputMode = "clipboard"
	OutputModeStdout    OutputMode = "stdout"
)

// Parsed is the typed result of CLI argv parsing. It captures the entire
// user intent — what to copy (Command Spec), how to render it (Verbose /
// Quiet / NoTree / Preview / Raw), where it goes (OutputMode), and any
// internal-preview state for fzf-spawned helper processes. The runtime
// pipeline reads from Parsed once at the start of run() and never mutates
// it; downstream callers receive narrower configs derived from Parsed via
// *FromParsedCommand conversion functions.
//
// Field grouping (in declaration order): identity / output sink, user
// presentation flags, internal preview-state hooks (for fzf reload
// commands), the canonicalized command Spec, and parser warnings to
// surface on stderr.
type Parsed struct {
	Action     Action
	Version    string
	Platform   string
	WorkingDir string
	OutputMode OutputMode

	Verbose                bool
	Quiet                  bool
	Headless               bool
	WithBinaries           bool
	Yes                    bool
	Raw                    bool
	Preview                bool
	NoTree                 bool
	NoBundle               bool
	TreePreview            bool
	PrediscoveredPath      string
	TreeInputDir           string
	TreeInputStem          string
	TreeTarget             string
	TreeKind               string
	TreeState              string
	FileSetSelectionPath   string
	FileSetSelectionStage  string
	FilePreview            bool
	FileSearchingPreview   bool
	FilePath               string
	ContentMatchList       bool
	RecentPreview          bool
	RecentData             string
	RecentSelect           string
	LinesPreview           bool
	SnippetBoundaryPreview bool
	BoundarySourcePath     string
	BoundaryKey            string
	SinkTogglePath         string
	SinkPreviewModePath    string
	SinkPreviewOutputPath  string
	SinkPreviewTreePath    string

	Command Spec

	Warnings []string
}

// IsInternalKind reports whether this Parsed represents an --internal-*
// helper invocation — the short-lived processes fzf spawns for tree /
// file / content / recent / lines / snippet-boundary / sink previews,
// or for prediscovered-checkpoint-driven runs. Internal-kind processes
// suppress interactive prompts (canPromptForChoice consumes this via
// Invocation.Internal).
//
// This is the sole internal-kind predicate. Root dispatch and
// InvocationFromParsed both consume it so preview helpers cannot drift from
// prompt-suppression behavior when a new internal command is added.
func (p Parsed) IsInternalKind() bool {
	return p.TreePreview || p.FilePreview || p.FileSearchingPreview ||
		p.ContentMatchList || p.SnippetBoundaryPreview || p.RecentPreview ||
		p.LinesPreview ||
		p.PrediscoveredPath != "" || p.TreeInputDir != "" ||
		p.FileSetSelectionPath != "" || p.FileSetSelectionStage != "" ||
		p.SinkTogglePath != "" || p.SinkPreviewModePath != ""
}
