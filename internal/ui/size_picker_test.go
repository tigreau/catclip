package ui

import (
	"reflect"
	"testing"

	"github.com/tigreau/catclip/internal/discovery"
)

func TestStartupMinSizeCandidatesIncludeNoMinimumAndLowerBounds(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "tiny.txt", SizeBytes: 512, SizeKnown: true},
		{RelPath: "one.txt", SizeBytes: 1024, SizeKnown: true},
		{RelPath: "two.txt", SizeBytes: 2048, SizeKnown: true},
	}

	candidates := startupMinSizeCandidates(entries)
	got := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		got = append(got, candidate.Token)
	}
	if want := []string{"0", "1", "2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("min candidate tokens = %v, want %v", got, want)
	}
	if len(candidates[0].Nums) != 0 {
		t.Fatalf("zero minimum preview should use bare --size nums, got %v", candidates[0].Nums)
	}
}

func TestStartupMaxSizeCandidatesIncludeNoMaximumAndValidUpperBounds(t *testing.T) {
	entries := []discovery.Entry{
		{RelPath: "tiny.txt", SizeBytes: 512, SizeKnown: true},
		{RelPath: "one.txt", SizeBytes: 1024, SizeKnown: true},
		{RelPath: "one-half.txt", SizeBytes: 1536, SizeKnown: true},
		{RelPath: "three.txt", SizeBytes: 3072, SizeKnown: true},
	}

	candidates := startupMaxSizeCandidates(entries, 1)
	got := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		got = append(got, candidate.Token)
	}
	if want := []string{sizePickerNoMaxToken, "1", "2", "3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("max candidate tokens = %v, want %v", got, want)
	}
	if got, want := candidates[0].Nums, []int{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("no-max candidate nums = %v, want %v", got, want)
	}
}

func TestStartupSizeBoundsFromRemaining(t *testing.T) {
	nums, consumed, err := startupSizeBoundsFromRemaining([]string{"0", "100", "--depth"})
	if err != nil {
		t.Fatalf("startupSizeBoundsFromRemaining returned error: %v", err)
	}
	if consumed != 2 {
		t.Fatalf("consumed = %d, want 2", consumed)
	}
	if want := []int{0, 100}; !reflect.DeepEqual(nums, want) {
		t.Fatalf("nums = %v, want %v", nums, want)
	}
}

func TestStartupSizeBoundsFromRemainingRejectsZeroMax(t *testing.T) {
	_, _, err := startupSizeBoundsFromRemaining([]string{"0", "0"})
	if err == nil {
		t.Fatal("expected zero max error")
	}
}
