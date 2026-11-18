package output

import (
	"reflect"
	"testing"
)

// Regressions found by the v0.8.0 AST comparison and fixed in v0.7.13 in the
// handwritten engine: labels and preprocessor lines left of a { } body must not
// end it, and type parameters before `(` must not hide a method.
func TestSnippetBodiesSurviveDedentedLabelsAndDirectives(t *testing.T) {
	tests := []struct {
		name  string
		ext   string
		lines []string
		match int
		want  []SnippetRange
	}{
		{
			name: "gofmt label at column 0 in a top-level function",
			ext:  ".go",
			lines: []string{
				"func Collect() {", // 1
				"\tfor {",          // 2
				"\t\tbreak loop",   // 3 match
				"\t}",              // 4
				"loop:",            // 5
				"\treturn",         // 6
				"}",                // 7
				"",                 // 8
				"func Next() {}",   // 9
			},
			match: 3,
			want:  []SnippetRange{{Start: 1, End: 7}},
		},
		{
			name: "C error labels and a statement on the label line",
			ext:  ".c",
			lines: []string{
				"static int probe(void)", // 1
				"{",                      // 2
				"\tif (bad)",             // 3
				"\t\tgoto out;",          // 4 match
				"err: cleanup();",        // 5
				"out:",                   // 6
				"\treturn err;",          // 7
				"}",                      // 8
			},
			match: 4,
			want:  []SnippetRange{{Start: 1, End: 8}},
		},
		{
			name: "preprocessor block inside a C function",
			ext:  ".c",
			lines: []string{
				"static int setup(void)", // 1
				"{",                      // 2
				"\tint x = 0;",           // 3 match
				"#ifdef CONFIG_X",        // 4
				"\tx = 1;",               // 5
				"#endif",                 // 6
				"\treturn x;",            // 7
				"}",                      // 8
			},
			match: 3,
			want:  []SnippetRange{{Start: 1, End: 8}},
		},
		{
			name: "directive after a function stays outside it",
			ext:  ".c",
			lines: []string{
				"static int first(void)",  // 1
				"{",                       // 2
				"\treturn 1;",             // 3 match
				"}",                       // 4
				"",                        // 5
				"#ifdef CONFIG_X",         // 6
				"static int second(void)", // 7
				"{",                       // 8
				"\treturn 2;",             // 9
				"}",                       // 10
				"#endif",                  // 11
			},
			match: 3,
			want:  []SnippetRange{{Start: 1, End: 4}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSnippetRanges(tt.lines, []int{tt.match}, profileForExt(tt.ext))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSnippetMethodsWithTypeParameters(t *testing.T) {
	lines := []string{
		"export class Store<T> {",    // 1
		"\tprivate items: T[] = [];", // 2
		"",                           // 3
		"\tpublic add<K extends keyof T>(key: K, value: T[K]): void {", // 4
		"\t\tthis.items.push(value as any);",                           // 5 match
		"\t}",                                                          // 6
		"",                                                             // 7
		"\tprotected static create<U>(): Store<U> {", // 8
		"\t\treturn new Store<U>();",                 // 9 match
		"\t}",                                        // 10
		"}",                                          // 11
	}
	for _, tt := range []struct {
		match int
		want  SnippetRange
	}{{5, SnippetRange{Start: 4, End: 6}}, {9, SnippetRange{Start: 8, End: 10}}} {
		got := buildSnippetRanges(lines, []int{tt.match}, profileForExt(".ts"))
		if !reflect.DeepEqual(got, []SnippetRange{tt.want}) {
			t.Fatalf("match %d: got %+v, want %+v", tt.match, got, tt.want)
		}
	}
	for _, tt := range []struct{ in, want string }{
		{"public add<T extends IDisposable>", "public add"},
		{"create<Map<K, V>>", "create"},
		{"bool operator>", "bool operator>"},
		{"plain", "plain"},
	} {
		if got := trimTrailingTypeParameters(tt.in); got != tt.want {
			t.Errorf("trimTrailingTypeParameters(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// C# properties and events have no `(` after their name; before v0.7.13 a
// match on one returned the whole class. Found by the ast-grep comparison on
// dotnet/eShop.
func TestSnippetCSharpPropertiesAndEvents(t *testing.T) {
	lines := []string{
		"public class Shapes", // 1
		"{",                   // 2
		"    public string Name { get; set; } = \"\";", // 3
		"",                                      // 4
		"    public int Count => _items.Count;", // 5
		"",                                      // 6
		"    public decimal Total",              // 7
		"    {",                                 // 8
		"        get { return _items.Sum(i => i.Price); }", // 9
		"    }",            // 10
		"",                 // 11
		"    [JsonIgnore]", // 12
		"    public event EventHandler? Changed;", // 13
		"", // 14
		"    private readonly List<Item> _items = new();", // 15
		"",                          // 16
		"    public int Total2 =>",  // 17
		"        _items.Count * 2;", // 18
		"}",                         // 19
	}
	for _, tt := range []struct {
		match int
		want  SnippetRange
	}{
		{3, SnippetRange{Start: 3, End: 3}},
		{5, SnippetRange{Start: 5, End: 5}},
		{9, SnippetRange{Start: 7, End: 10}},
		{13, SnippetRange{Start: 12, End: 13}},
		{18, SnippetRange{Start: 17, End: 18}},
		{15, SnippetRange{Start: 1, End: 19}}, // a plain field stays with its class
	} {
		got := buildSnippetRanges(lines, []int{tt.match}, profileForExt(".cs"))
		if !reflect.DeepEqual(got, []SnippetRange{tt.want}) {
			t.Errorf("match %d: got %+v, want %+v", tt.match, got, tt.want)
		}
	}
}

// Found by the ast-grep comparison on fmtlib/fmt: a directive straight after
// `{` or straight before `}` must not cut the function.
func TestSnippetDirectivesAtBodyEdges(t *testing.T) {
	lines := []string{
		"TEST(file_test, open_retry) {",         // 1
		"#  ifndef _WIN32",                      // 2
		"  write_file(\"temp\");",               // 3 match
		"#  endif",                              // 4
		"}",                                     // 5
		"",                                      // 6
		"int(test::fileno)(FILE* stream) {",     // 7
		"  EMULATE_EINTR(fileno, -1);",          // 8 match
		"#ifdef fileno",                         // 9
		"  return FMT_POSIX(fileno(stream));",   // 10
		"#else",                                 // 11
		"  return ::FMT_POSIX(fileno(stream));", // 12
		"#endif",                                // 13
		"}",                                     // 14
	}
	for _, tt := range []struct {
		match int
		want  SnippetRange
	}{{3, SnippetRange{Start: 1, End: 5}}, {8, SnippetRange{Start: 7, End: 14}}} {
		got := buildSnippetRanges(lines, []int{tt.match}, profileForExt(".cc"))
		if !reflect.DeepEqual(got, []SnippetRange{tt.want}) {
			t.Errorf("match %d: got %+v, want %+v", tt.match, got, tt.want)
		}
	}
}

func TestSnippetColumnZeroCommentInsideBody(t *testing.T) {
	lines := []string{
		"setup() {",              // 1
		"  local x=1",            // 2 match
		"# temporarily disabled", // 3
		"  echo \"$x\"",          // 4
		"}",                      // 5
		"",                       // 6
		"# helpers",              // 7
		"other() {",              // 8
		"  echo other",           // 9
		"}",                      // 10
	}
	got := buildSnippetRanges(lines, []int{2}, profileForExt(".sh"))
	if want := []SnippetRange{{Start: 1, End: 5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
