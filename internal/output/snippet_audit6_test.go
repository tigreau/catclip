package output

import (
	"reflect"
	"testing"
)

// TestSnippetVuePascalComponent pins that a Vue PascalCase component <Input> is
// NOT folded to the native void <input> and discarded: a match inside it returns
// the component, not the whole template.
func TestSnippetVuePascalComponent(t *testing.T) {
	lines := []string{
		"<template>",       // 1
		"  <Input>",        // 2
		"    needle",       // 3  match
		"  </Input>",       // 4
		"  <span>x</span>", // 5
		"</template>",      // 6
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".vue"))
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vue PascalCase component: got %v, want component {2,4}", got)
	}
}

// TestSnippetSvelteNativeStillHTML pins that native lowercase tags in a Svelte
// file still get HTML treatment: a native void <input> before a <p> does not
// block the paragraph resolving under implied-close rules.
func TestSnippetSvelteNativeStillHTML(t *testing.T) {
	lines := []string{
		"<div>",      // 1
		"  <input>",  // 2  native void: not pushed
		"  <p>",      // 3
		"    needle", // 4  match
		"</div>",     // 5
	}
	got := buildSnippetRanges(lines, []int{4}, profileForExt(".svelte"))
	want := []SnippetRange{{Start: 3, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("svelte native tag: got %v, want <p> {3,4}", got)
	}
}

// TestSnippetVueScriptComponentNotRawText pins that a PascalCase <Script>
// component is not mistaken for the native raw-text <script> region.
func TestSnippetVueScriptComponentNotRawText(t *testing.T) {
	lines := []string{
		"<template>",        // 1
		"  <Script>",        // 2  component, NOT native raw-text <script>
		`    <b>needle</b>`, // 3  match: real markup, must nest normally
		"  </Script>",       // 4
		"</template>",       // 5
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".vue"))
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vue <Script> component: got %v, want component {2,4}", got)
	}
}
