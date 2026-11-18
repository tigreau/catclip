package ui

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/tigreau/catclip/internal/command"
	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/git"
	"github.com/tigreau/catclip/internal/output"
	"github.com/tigreau/catclip/internal/platform"
)

func TestTokenLimitStartupWalkers(t *testing.T) {
	args := []string{"--token-limit", "12", "src"}
	frame0, _, _, err0 := nextStartupInteractiveFrame(&discovery.Resolver{}, nil, args)
	if err0 != nil || !reflect.DeepEqual(frame0.TargetTokens, []string{"src"}) || !reflect.DeepEqual(frame0.StartArgs, []string{"--token-limit", "12"}) {
		t.Fatalf("frame %+v, %v", frame0, err0)
	}
	leading, err0 := parseStartupInputTokens(args)
	if err0 != nil || !reflect.DeepEqual(leading.targets, []string{"src"}) {
		t.Fatalf("leading budget: %+v, %v", leading, err0)
	}

	if startupHasUnresolvedScope(args) {
		t.Fatal("budget was treated as a target")
	}
	selected, _ := startupFrameCurrentScopeSelections(args)
	if !reflect.DeepEqual(selected, []string{"src"}) || startupFrameCurrentScopeHasModifier(args) {
		t.Fatalf("wrong scope %v", selected)
	}
	frame := startupInclusionTargetFrame(startupInteractiveFrame{StartArgs: args}, []string{"--no-ignore"})
	if !reflect.DeepEqual(frame.StartArgs, []string{"--token-limit", "12"}) {
		t.Fatalf("lost budget on inclusion: %v", frame.StartArgs)
	}
	if startupFrameTargetPrompt([]string{"src", "--then", "--token-limit", "12"}) != "then> " {
		t.Fatal("budget hid next-scope prompt")
	}
	parsed, err := parseStartupInputTokens([]string{"src", "--token-limit", "12", "--only", "*.go"})
	if err != nil || !reflect.DeepEqual(parsed.targets, []string{"src"}) || !reflect.DeepEqual(parsed.modifiers, []string{"--token-limit", "12", "--only", "*.go"}) {
		t.Fatalf("%+v, %v", parsed, err)
	}
}

func TestTokenLimitSinkPreviewParity(t *testing.T) {
	project := setupTestProject(t, map[string]string{"a.go": "package main\nvar x = 1\n", "b.go": "package main\nvar y = 2\n"})
	t.Chdir(project)
	scopeViewMemoReset()
	defer scopeViewMemoReset()
	for _, args := range [][]string{{".", "--token-limit", "2"}, {".", "--metadata", "--token-limit", "2"}, {"a.go", "--then", "b.go", "--metadata", "--token-limit", "8"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx, err := buildStartupSinkPickerContext(args)
			if err != nil {
				t.Fatal(err)
			}
			measurement := measureStartupSinkPayload(ctx)
			if measurement.Err != nil {
				t.Fatal(measurement.Err)
			}
			var want bytes.Buffer
			if err := output.WriteOutputPlanPayload(&want, ctx.Emit, ctx.Plan); err != nil {
				t.Fatal(err)
			}
			preview, err := renderSinkPreviewWithModeContext(context.Background(), ctx, sinkPreviewModeOutputText, 64*1024)
			if err != nil {
				t.Fatal(err)
			}
			if got := stripANSIForTest(string(preview.Body)); got != want.String() {
				t.Fatalf("preview differs:\n%q\n%q", got, want.String())
			}
			if !strings.Contains(want.String(), "output truncated") || ctx.Report.Tokens > ctx.Config.TokenLimit {
				t.Fatalf("output %q, report %+v", want.String(), ctx.Report)
			}
		})
	}
}

func TestTokenLimitReplacesWarningAndPrompt(t *testing.T) {
	for _, limit := range []int64{0, 200000} {
		var out bytes.Buffer
		policy := command.EmissionDefault
		if limit == 0 {
			policy = command.EmissionAlways
		}
		proceed, err := WriteNormalDiagnostics(RenderConfig{NoTree: true, TokenLimit: limit}, git.Context{}, output.Plan{}, output.Report{Tokens: 100001}, policy, &out, &out, platform.Palette{}, platform.Palette{})
		if err != nil || !proceed || strings.Contains(out.String(), "may exceed") != (limit == 0) {
			t.Fatalf("limit %d: %v %v %q", limit, proceed, err, out.String())
		}
	}
}
