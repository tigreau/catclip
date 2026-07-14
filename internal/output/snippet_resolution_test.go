package output

import (
	"reflect"
	"testing"
)

func TestBuildSnippetRangesFindsSmallestEnclosingDeclaration(t *testing.T) {
	tests := []struct {
		name    string
		lines   []string
		matched []int
		want    []SnippetRange
	}{
		{
			name: "go multiline signature from interior match",
			lines: []string{
				"package sample",
				"",
				"// Example validates value.",
				"func Example(",
				"\tvalue string,",
				") error {",
				"\tif value == \"\" {",
				"\t\treturn errEmpty",
				"\t}",
				"",
				"\treturn nil",
				"}",
				"",
				"func Next() {}",
			},
			matched: []int{8},
			want:    []SnippetRange{{Start: 3, End: 12}},
		},
		{
			name: "java chooses method rather than class",
			lines: []string{
				"@Service",
				"public class BillingService {",
				"    private final Store store;",
				"",
				"    @Transactional",
				"    public Receipt charge(",
				"        Account account,",
				"        Money amount",
				"    ) throws PaymentException {",
				"        store.reserve(account);",
				"",
				"        return new Receipt(amount);",
				"    }",
				"}",
			},
			matched: []int{10},
			want:    []SnippetRange{{Start: 5, End: 13}},
		},
		{
			name: "csharp allman method and attribute",
			lines: []string{
				"public class Controller",
				"{",
				"    [HttpGet]",
				"    public Result Get()",
				"    {",
				"        return service.Load();",
				"    }",
				"}",
			},
			matched: []int{6},
			want:    []SnippetRange{{Start: 3, End: 7}},
		},
		{
			name: "c function",
			lines: []string{
				"static int compute(int value) {",
				"    int adjusted = value + 1;",
				"",
				"    return adjusted;",
				"}",
				"",
				"static int next(void) { return 0; }",
			},
			matched: []int{2},
			want:    []SnippetRange{{Start: 1, End: 5}},
		},
		{
			name: "typescript arrow",
			lines: []string{
				"const resolve = (",
				"  input: Input,",
				") => {",
				"  const normalized = normalize(input);",
				"",
				"  return normalized;",
				"};",
			},
			matched: []int{4},
			want:    []SnippetRange{{Start: 1, End: 7}},
		},
		{
			name: "python decorator and dedent",
			lines: []string{
				"@transactional",
				"def save(",
				"    record: Record,",
				") -> Result:",
				"    prepared = prepare(record)",
				"",
				"    return persist(prepared)",
				"next_value = 1",
			},
			matched: []int{5},
			want:    []SnippetRange{{Start: 1, End: 7}},
		},
		{
			name: "unknown top level syntax falls back to paragraph",
			lines: []string{
				"setting = enabled",
				"timeout = 30",
				"",
				"next = true",
			},
			matched: []int{2},
			want:    []SnippetRange{{Start: 1, End: 2}},
		},
		{
			name: "single line declaration remains one structural unit",
			lines: []string{
				"class Service {",
				"    first() { return 1; }",
				"    second() { return 2; }",
				"}",
			},
			matched: []int{2},
			want:    []SnippetRange{{Start: 2, End: 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildSnippetRanges(tt.lines, tt.matched, defaultProfile); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("buildSnippetRanges() = %v, want %v; declarations=%v", got, tt.want, buildDeclarationRanges(tt.lines, defaultProfile.lineComments, nil))
			}
		})
	}
}

// TestSnippetProfileStripsHashCommentForPython pins the Phase 1 per-extension
// comment fix: a trailing `#` comment on a Python declaration line hides the
// `:` opener unless the profile strips `#`. The .py profile recognizes the
// class; the default (//-comment) profile falls back to the wider paragraph.
func TestSnippetProfileStripsHashCommentForPython(t *testing.T) {
	lines := []string{
		"class Foo:  # a trailing comment",
		"    a = 1",
		"    b = 2",
		"    c = 3",
		"z = 1",
	}
	want := []SnippetRange{{Start: 1, End: 4}}
	if got := buildSnippetRanges(lines, []int{2}, profileForExt(".py")); !reflect.DeepEqual(got, want) {
		t.Fatalf("python profile: got %v, want %v (class should be recognized)", got, want)
	}
	if got := buildSnippetRanges(lines, []int{2}, defaultProfile); reflect.DeepEqual(got, want) {
		t.Fatalf("default profile should not strip #, so the class stays unrecognized; got %v", got)
	}
}

func TestBuildSnippetRangesMergesNestedAndRepeatedMatches(t *testing.T) {
	lines := []string{
		"class Service {",
		"    run() {",
		"        first();",
		"",
		"        second();",
		"    }",
		"}",
	}

	got := buildSnippetRanges(lines, []int{1, 3, 5}, defaultProfile)
	want := []SnippetRange{{Start: 1, End: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSnippetRanges() = %v, want %v", got, want)
	}
}

func TestDeclarationRecognitionRejectsControlFlowAndCalls(t *testing.T) {
	lines := []string{
		"if ready {",
		"    execute();",
		"}",
		"",
		"foreach (var item in items) {",
		"    execute(item);",
		"}",
		"",
		"synchronized (lock) {",
		"    execute();",
		"}",
		"",
		"configure(",
		"    value,",
		")",
	}

	if got := buildDeclarationRanges(lines, defaultProfile.lineComments, nil); len(got) != 0 {
		t.Fatalf("buildDeclarationRanges() = %v, want no declarations", got)
	}
}

func TestDefaultSnippetUsesDeclarationInsteadOfWiderParagraph(t *testing.T) {
	lines := []string{
		"class Service {",
		"    run() {",
		"        first();",
		"    }",
		"}",
	}
	got := buildSnippetRanges(lines, []int{3}, defaultProfile)
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSnippetRanges() = %v, want %v", got, want)
	}
}
