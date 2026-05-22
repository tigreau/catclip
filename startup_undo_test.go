package catclip

import (
	"reflect"
	"testing"
)

func TestNextStartupInteractiveFrameRoutesRepresentativeStates(t *testing.T) {
	tests := []struct {
		name          string
		currentArgs   []string
		pendingArgs   []string
		wantKind      startupInteractiveFrameKind
		wantStartArgs []string
		wantPending   []string
		wantTargets   []string
		wantChoice    []string
		wantPrompt    string
		wantDone      bool
		wantDoneArgs  []string
	}{
		{
			name:       "empty command opens initial target picker",
			wantKind:   startupInteractiveFrameTarget,
			wantPrompt: "select> ",
		},
		{
			name:        "bare global flag still picks target before appending flag",
			pendingArgs: []string{"-q"},
			wantKind:    startupInteractiveFrameTarget,
			wantPending: []string{"-q"},
			wantPrompt:  "select> ",
		},
		{
			name:          "typed target token opens target frame for token",
			pendingArgs:   []string{"src"},
			wantKind:      startupInteractiveFrameTarget,
			wantPending:   nil,
			wantTargets:   []string{"src"},
			wantPrompt:    "select> ",
			wantStartArgs: nil,
		},
		{
			name:          "trailing modifier placeholder opens modifier menu",
			currentArgs:   []string{"src"},
			pendingArgs:   []string{"--"},
			wantKind:      startupInteractiveFrameModifier,
			wantStartArgs: []string{"src"},
			wantPending:   nil,
		},
		{
			name:          "placeholder chain preserves remaining placeholder",
			currentArgs:   []string{"src"},
			pendingArgs:   []string{"--", "--"},
			wantKind:      startupInteractiveFrameModifier,
			wantStartArgs: []string{"src"},
			wantPending:   []string{"--"},
		},
		{
			name:          "trailing then opens next-scope target picker",
			currentArgs:   []string{"src", "--then"},
			wantKind:      startupInteractiveFrameTarget,
			wantStartArgs: []string{"src", "--then"},
			wantPrompt:    "then> ",
		},
		{
			name:          "typed only stage routes to stage frame",
			currentArgs:   []string{"src"},
			pendingArgs:   []string{"--only", "main"},
			wantKind:      startupInteractiveFrameStage,
			wantStartArgs: []string{"src"},
			wantPending:   []string{"main"},
			wantChoice:    []string{"--only"},
		},
		{
			name:         "settled scope is done",
			currentArgs:  []string{"src", "--paths"},
			wantDone:     true,
			wantDoneArgs: []string{"src", "--paths"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame, doneArgs, done, err := nextStartupInteractiveFrame(nil, tt.currentArgs, tt.pendingArgs)
			if err != nil {
				t.Fatalf("nextStartupInteractiveFrame returned error: %v", err)
			}
			if done != tt.wantDone {
				t.Fatalf("done = %v, want %v", done, tt.wantDone)
			}
			if done {
				if !reflect.DeepEqual(doneArgs, tt.wantDoneArgs) {
					t.Fatalf("doneArgs = %#v, want %#v", doneArgs, tt.wantDoneArgs)
				}
				return
			}
			if frame.Kind != tt.wantKind {
				t.Fatalf("kind = %q, want %q", frame.Kind, tt.wantKind)
			}
			if !reflect.DeepEqual(frame.StartArgs, tt.wantStartArgs) {
				t.Fatalf("StartArgs = %#v, want %#v", frame.StartArgs, tt.wantStartArgs)
			}
			if !reflect.DeepEqual(frame.PendingArgs, tt.wantPending) {
				t.Fatalf("PendingArgs = %#v, want %#v", frame.PendingArgs, tt.wantPending)
			}
			if !reflect.DeepEqual(frame.TargetTokens, tt.wantTargets) {
				t.Fatalf("TargetTokens = %#v, want %#v", frame.TargetTokens, tt.wantTargets)
			}
			if !reflect.DeepEqual(frame.ChoiceArgs, tt.wantChoice) {
				t.Fatalf("ChoiceArgs = %#v, want %#v", frame.ChoiceArgs, tt.wantChoice)
			}
			if tt.wantPrompt != "" && frame.TargetPrompt != tt.wantPrompt {
				t.Fatalf("TargetPrompt = %q, want %q", frame.TargetPrompt, tt.wantPrompt)
			}
		})
	}
}
