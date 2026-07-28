package output

import (
	"reflect"
	"testing"
)

func TestSnippetInlineJavaReturnAnnotations(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		match int
		want  []SnippetRange
	}{
		{
			name: "package private method",
			lines: []string{
				"class ConstructorResolver {",                    // 1
				"    @Nullable Object resolveAutowiredArgument(", // 2
				"        MethodParameter parameter,",             // 3
				"        Class<?> fallbackType",                  // 4
				"    ) {",                                        // 5
				"        return resolveDependency(parameter);",   // 6 match
				"    }", // 7
				"}",     // 8
			},
			match: 6,
			want:  []SnippetRange{{Start: 2, End: 7}},
		},
		{
			name: "annotation arguments and modifier",
			lines: []string{
				"class Registry {", // 1
				`    @Qualifier(value = "primary") public Object load() {`, // 2
				"        return getBean();",                                // 3 match
				"    }",                                                    // 4
				"}",                                                        // 5
			},
			match: 3,
			want:  []SnippetRange{{Start: 2, End: 4}},
		},
		{
			name: "annotation-only line stays attached",
			lines: []string{
				"class Registry {",           // 1
				"    @Override",              // 2
				"    public Object load() {", // 3
				"        return getBean();",  // 4 match
				"    }",                      // 5
				"}",                          // 6
			},
			match: 4,
			want:  []SnippetRange{{Start: 2, End: 5}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSnippetRanges(tt.lines, []int{tt.match}, profileForExt(".java"))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSnippetAttachesCompleteBlockComments(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		match int
		want  []SnippetRange
	}{
		{
			name: "exported binding with punctuation in jsdoc",
			lines: []string{
				"/**",                                    // 1
				" * Renders values returned by load()).", // 2
				" * Example: { enabled: true }",          // 3
				" */",                                    // 4
				"export const View = () => {",            // 5
				"  return <div>{load()}</div>",           // 6 match
				"}",                                      // 7
			},
			match: 6,
			want:  []SnippetRange{{Start: 1, End: 7}},
		},
		{
			name: "interface comment",
			lines: []string{
				"/**",                       // 1
				" * Public input contract.", // 2
				" */",                       // 3
				"export interface Props {",  // 4
				"  value: string",           // 5 match
				"}",                         // 6
			},
			match: 5,
			want:  []SnippetRange{{Start: 1, End: 6}},
		},
		{
			name: "nested method comment",
			lines: []string{
				"class Controller {",               // 1
				"  /**",                            // 2
				"   * Runs callback(() => value).", // 3
				"   */",                            // 4
				"  execute() {",                    // 5
				"    return load()",                // 6 match
				"  }",                              // 7
				"}",                                // 8
			},
			match: 6,
			want:  []SnippetRange{{Start: 2, End: 7}},
		},
		{
			name: "blank line keeps earlier comment detached",
			lines: []string{
				"/** unrelated documentation */", // 1
				"",                               // 2
				"export function run() {",        // 3
				"  return load()",                // 4 match
				"}",                              // 5
			},
			match: 4,
			want:  []SnippetRange{{Start: 3, End: 5}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSnippetRanges(tt.lines, []int{tt.match}, profileForExt(".tsx"))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSnippetNamedObjectAndArrayBindings(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		match int
		want  []SnippetRange
	}{
		{
			name: "exported object with nested literals and blank lines",
			lines: []string{
				"/** Edit source commands. */",     // 1
				"export const EditSources = {",     // 2
				`  label: "literal } stays text",`, // 3
				"  primary: {",                     // 4
				"    command: 'edit',",             // 5 match
				"  },",                             // 6
				"",                                 // 7
				"  secondary: [",                   // 8
				"    'preview',",                   // 9
				"  ],",                             // 10
				"}",                                // 11
			},
			match: 5,
			want:  []SnippetRange{{Start: 1, End: 11}},
		},
		{
			name: "array ignores delimiters in comments and includes separate semicolon",
			lines: []string{
				"export const commands = [", // 1
				`  "text ] only",`,          // 2
				"  /* ] ignored */",         // 3
				"  {",                       // 4
				"    id: 'run',",            // 5 match
				"  },",                      // 6
				"]",                         // 7
				";",                         // 8
			},
			match: 5,
			want:  []SnippetRange{{Start: 1, End: 8}},
		},
		{
			name: "local object is smaller than its function",
			lines: []string{
				"function configure() {", // 1
				"  const options = {",    // 2
				"    enabled: true,",     // 3 match
				"",                       // 4
				"    retries: 2,",        // 5
				"  }",                    // 6
				"  return options",       // 7
				"}",                      // 8
			},
			match: 3,
			want:  []SnippetRange{{Start: 2, End: 6}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSnippetRanges(tt.lines, []int{tt.match}, profileForExt(".ts"))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSnippetLiteralBindingNegativeCases(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
	}{
		{
			name: "destructured left side",
			lines: []string{
				"function configure() {",  // 1
				"  const { primary } = {", // 2
				"    primary: load(),",    // 3 match
				"",                        // 4
				"    fallback: true,",     // 5
				"  }",                     // 6
				"  return primary",        // 7
				"}",                       // 8
			},
		},
		{
			name: "typed object assignment",
			lines: []string{
				"function configure() {",      // 1
				"  const options: Config = {", // 2
				"    primary: load(),",        // 3 match
				"",                            // 4
				"    fallback: true,",         // 5
				"  }",                         // 6
				"  return options",            // 7
				"}",                           // 8
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSnippetRanges(tt.lines, []int{3}, profileForExt(".ts"))
			want := []SnippetRange{{Start: 1, End: 8}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want outer function %v", got, want)
			}
		})
	}
}

func TestSnippetSmartPreservesRepresentativeMatchKinds(t *testing.T) {
	lines := []string{
		"/** load appears in documentation. */", // 1 comment
		"export function run() {",               // 2 definition
		`  const label = "load in a string"`,    // 3 string
		"  return load()",                       // 4 usage
		"}",                                     // 5
	}
	matches := []int{1, 2, 3, 4}
	ranges := buildSnippetRanges(lines, matches, profileForExt(".ts"))
	for _, match := range matches {
		if !rangeContainsLine(ranges, match) {
			t.Fatalf("smart ranges %v lost matched line %d", ranges, match)
		}
	}
}

func rangeContainsLine(ranges []SnippetRange, line int) bool {
	for _, candidate := range ranges {
		if candidate.Start <= line && line <= candidate.End {
			return true
		}
	}
	return false
}
