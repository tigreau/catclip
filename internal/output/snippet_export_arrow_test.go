package output

import (
	"reflect"
	"testing"
)

// TestSnippetExportConstArrowComponent pins recognition of the dominant React
// component shape: a match inside `export const X = (...) => {` returns the
// whole component, not a blank-line paragraph slice of its opening lines.
func TestSnippetExportConstArrowComponent(t *testing.T) {
	lines := []string{
		"import { useAuth } from './auth'", // 1
		"",                                 // 2
		"export const CartProvider = ({ children }: { children: ReactNode }) => {", // 3
		"    const [items, setItems] = useState([])",                               // 4
		"    const { isAuthenticated } = useAuth()",                                // 5  match
		"",                                   // 6  interior blank line
		"    return <Cart>{children}</Cart>", // 7
		"}",                                  // 8
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 3, End: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("export const arrow: got %v, want whole component %v", got, want)
	}
}

// TestSnippetMultilineGenericArrowComponent pins the Payload CMS shape: the
// component's generic parameter spans lines, so the declaration line ends in
// `<` and the `= (props) => {` opener arrives lines later.
func TestSnippetMultilineGenericArrowComponent(t *testing.T) {
	lines := []string{
		"export const DrawerContent: React.FC<", // 1
		"  Props & {",                           // 2
		"    extra: boolean",                    // 3
		"  }",                                   // 4
		"> = (props) => {",                      // 5
		"  const { user } = useAuth()",          // 6  match
		"",                                      // 7
		"  return <div>{props.title}</div>",     // 8
		"}",                                     // 9
	}
	got := buildSnippetRanges(lines, []int{6}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multiline generic arrow: got %v, want whole component %v", got, want)
	}
}

// TestSnippetSplitTypedObjectNotADeclaration pins the audit finding-1
// reproducer: a split typed-object declaration (`const options: {`) must NOT
// become a false arrow candidate, or its later-arrow "opener" truncates the
// correctly recognized enclosing function.
func TestSnippetSplitTypedObjectNotADeclaration(t *testing.T) {
	lines := []string{
		"function setup() {",         // 1
		"  const options: {",         // 2
		"    enabled: boolean",       // 3
		"  } = {",                    // 4
		"    enabled: useAuth,",      // 5  match
		"  }",                        // 6
		"  const callback = () => {", // 7
		"    run()",                  // 8
		"  }",                        // 9
		"  finalize()",               // 10
		"}",                          // 11
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".ts"))
	want := []SnippetRange{{Start: 1, End: 11}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split typed object: got %v, want outer function %v", got, want)
	}
}

// TestSnippetGenericObjectTypeArrowComponent pins the `React.FC<{` shape: the
// generic and an inline object type open on the declaration line.
func TestSnippetGenericObjectTypeArrowComponent(t *testing.T) {
	lines := []string{
		"export const LoginForm: React.FC<{", // 1
		"  prefillEmail?: string",            // 2
		"  searchParams: Params",             // 3
		"}> = ({ prefillEmail }) => {",       // 4
		"  const { user } = useAuth()",       // 5  match
		"",                                   // 6
		"  return <form />",                  // 7
		"}",                                  // 8
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("React.FC<{ component: got %v, want whole component %v", got, want)
	}
}

// TestSnippetCommentOnlyLineInsideGenericHeader pins that a line-comment-only
// row inside a multi-line generic header does not end the header scan.
func TestSnippetCommentOnlyLineInsideGenericHeader(t *testing.T) {
	lines := []string{
		"export const SingleValue: React.FC<", // 1
		"  {",                                 // 2
		"    // TODO fix module resolution",   // 3  comment-only header row
		"    customProps: AdapterProps",       // 4
		"  } & SingleValueProps",              // 5
		"> = (props) => {",                    // 6
		"  const { permissions } = useAuth()", // 7  match
		"  return <div />",                    // 8
		"}",                                   // 9
	}
	got := buildSnippetRanges(lines, []int{7}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comment in generic header: got %v, want whole component %v", got, want)
	}
}

func TestSnippetArrowTypedPropInsideGenericHeader(t *testing.T) {
	lines := []string{
		"export const ActionButton: React.FC<{", // 1
		"  onClick: () => void",                 // 2  type arrow, not implementation
		"  label: string",                       // 3  must not abort the header
		"}> = ({ onClick, label }) => {",        // 4
		"  const { user } = useAuth()",          // 5  match
		"",                                      // 6
		"  return <button onClick={onClick}>{label}</button>", // 7
		"}", // 8
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arrow-typed prop: got %v, want whole component %v", got, want)
	}
}

func TestSnippetParenFreeArrowComponent(t *testing.T) {
	lines := []string{
		"export const UserBadge = props => {", // 1
		"  const { user } = useAuth()",        // 2  match
		"",                                    // 3
		"  return <span>{props.name}</span>",  // 4
		"}",                                   // 5
	}
	got := buildSnippetRanges(lines, []int{2}, profileForExt(".jsx"))
	want := []SnippetRange{{Start: 1, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paren-free arrow: got %v, want whole component %v", got, want)
	}
}

func TestSnippetBlockCommentInsideGenericHeader(t *testing.T) {
	lines := []string{
		"export const LogoutClient: React.FC<{", // 1
		"  /**",                                 // 2
		"   * Called after logout succeeds).",   // 3  ')' must not close the header
		"   */",                                 // 4
		"  onLogout: () => void",                // 5  type arrow must not be implementation
		"  redirectTo: string",                  // 6
		"}> = props => {",                       // 7  paren-free implementation arrow
		"  const { user } = useAuth()",          // 8  match
		"",                                      // 9
		"  return <button>{props.redirectTo}</button>", // 10
		"}", // 11
	}
	got := buildSnippetRanges(lines, []int{8}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 11}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("block comment in generic header: got %v, want whole component %v", got, want)
	}
}

func TestSnippetArrowTextInStringIsNotADeclaration(t *testing.T) {
	lines := []string{
		"function setup() {",          // 1
		"  const marker = \"x => {\"", // 2  syntax-like text, not an arrow
		"  const auth = useAuth()",    // 3  match
		"",                            // 4
		"  finalize(marker, auth)",    // 5
		"}",                           // 6
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".ts"))
	want := []SnippetRange{{Start: 1, End: 6}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("quoted arrow text: got %v, want outer function %v", got, want)
	}
}

func TestSnippetForwardRefArrowComponent(t *testing.T) {
	lines := []string{
		"const Input = React.forwardRef<HTMLInputElement, InputProps>(", // 1
		"  ({ className, type, ...props }, ref) => {",                   // 2
		"    const { user } = useAuth()",                                // 3  match
		"",                                                              // 4
		"    return <input ref={ref} type={type} {...props} />", // 5
		"  },", // 6
		")",    // 7
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardRef arrow: got %v, want whole wrapped component %v", got, want)
	}
}

func TestSnippetMemoArrowComponent(t *testing.T) {
	lines := []string{
		"export const UserCard = memo(({ user }: Props) => {", // 1
		"  const auth = useAuth()",                            // 2  match
		"  return <article>{user.name}</article>",             // 3
		"})", // 4
	}
	got := buildSnippetRanges(lines, []int{2}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("memo arrow: got %v, want whole wrapped component %v", got, want)
	}
}

func TestSnippetTypedArrowReturningObjectIsNotBodyOpener(t *testing.T) {
	lines := []string{
		"function setup() {",        // 1
		"  const factory: () => {",  // 2  type signature, not a body
		"    auth: Auth",            // 3
		"  } = createFactory()",     // 4
		"  const auth = useAuth()",  // 5  match
		"",                          // 6
		"  finalize(factory, auth)", // 7
		"}",                         // 8
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".ts"))
	want := []SnippetRange{{Start: 1, End: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typed object return: got %v, want outer function %v", got, want)
	}
}

func TestSnippetExportFunctionComponent(t *testing.T) {
	lines := []string{
		"export function AccountPanel({ user }: Props) {", // 1
		"  const auth = useAuth()",                        // 2  match
		"",                                                // 3
		"  return <section>{user.name}</section>", // 4
		"}", // 5
	}
	got := buildSnippetRanges(lines, []int{2}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("export function: got %v, want whole component %v", got, want)
	}
}

func TestSnippetExportDefaultAsyncFunctionComponent(t *testing.T) {
	lines := []string{
		"export default async function AccountPage({ user }: Props) {", // 1
		"  const auth = useAuth()",                                     // 2  match
		"  return <section>{user.name}</section>",                      // 3
		"}", // 4
	}
	got := buildSnippetRanges(lines, []int{2}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("export default async function: got %v, want whole component %v", got, want)
	}
}

func TestSnippetForwardRefFunctionComponent(t *testing.T) {
	lines := []string{
		"const Input = forwardRef<HTMLInputElement, InputProps>(function Input(props, ref) {", // 1
		"  const id = useId()", // 2  match
		"",                     // 3
		"  return <input id={id} ref={ref} {...props} />", // 4
		"})", // 5
	}
	got := buildSnippetRanges(lines, []int{2}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardRef function: got %v, want whole component %v", got, want)
	}
}

func TestSnippetMultilineForwardRefFunctionComponent(t *testing.T) {
	lines := []string{
		"export const TextArea = forwardRef<HTMLTextAreaElement, Props>(", // 1
		"  function TextArea(",   // 2
		"    props,",             // 3
		"    ref,",               // 4
		"  ) {",                  // 5
		"    const id = useId()", // 6  match
		"    return <textarea id={id} ref={ref} {...props} />", // 7
		"  },", // 8
		")",    // 9
	}
	got := buildSnippetRanges(lines, []int{6}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multiline forwardRef function: got %v, want whole component %v", got, want)
	}
}

func TestSnippetMemoMultilineDestructuredArrowComponent(t *testing.T) {
	lines := []string{
		"const ScheduleDetails = memo(",  // 1
		"  ({",                           // 2
		"    schedule,",                  // 3
		"    user,",                      // 4
		"  }: Props) => {",               // 5
		"    const locale = useLocale()", // 6  match
		"    return <View />",            // 7
		"  },",                           // 8
		")",                              // 9
	}
	got := buildSnippetRanges(lines, []int{6}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multiline memo arrow: got %v, want whole component %v", got, want)
	}
}

func TestSnippetGenericArrowBinding(t *testing.T) {
	lines := []string{
		"const Schedule = <",                        // 1
		"  TFieldValues extends FieldValues,",       // 2
		"  TPath extends FieldPath<TFieldValues>,",  // 3
		">(props: Props<TFieldValues, TPath>) => {", // 4
		"  const query = useMeQuery()",              // 5  match
		"  return <ScheduleView {...props} />",      // 6
		"}",                                         // 7
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generic arrow binding: got %v, want whole component %v", got, want)
	}
}

func TestSnippetGenericFunctionExpressionBinding(t *testing.T) {
	lines := []string{
		"export const SelectField = function SelectField<", // 1
		"  Option,",                                // 2
		"  IsMulti extends boolean = false,",       // 3
		">(props: SelectProps<Option, IsMulti>) {", // 4
		"  const locale = useLocale()",             // 5  match
		"  return <Select {...props} />",           // 6
		"}",                                        // 7
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generic function binding: got %v, want whole component %v", got, want)
	}
}

func TestSnippetBlankLineInsideGenericProps(t *testing.T) {
	lines := []string{
		"export const VersionLabel: React.FC<{", // 1
		"  current?: Version",                   // 2
		"",                                      // 3  intentional grouping
		"  doc: Document",                       // 4
		"}> = ({ current, doc }) => {",          // 5
		"  const locale = useLocale()",          // 6  match
		"  return <Pill doc={doc} />",           // 7
		"}",                                     // 8
	}
	got := buildSnippetRanges(lines, []int{6}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blank generic prop line: got %v, want whole component %v", got, want)
	}
}

func TestSnippetBlankLineInsideFunctionParameters(t *testing.T) {
	lines := []string{
		"export default function Popover({", // 1
		"  anchor,",                         // 2
		"  children,",                       // 3
		"",                                  // 4  intentional grouping
		"  origin = defaultOrigin,",         // 5
		"}: Props) {",                       // 6
		"  const theme = useTheme()",        // 7  match
		"  return <View>{children}</View>",  // 8
		"}",                                 // 9
	}
	got := buildSnippetRanges(lines, []int{7}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blank function parameter line: got %v, want whole component %v", got, want)
	}
}

func TestSnippetSplitGenericForwardRefComponent(t *testing.T) {
	lines := []string{
		"const Suggestion = React.forwardRef<",   // 1
		"  HTMLDivElement,",                      // 2
		"  SuggestionProps",                      // 3
		">(({ item }, ref) => {",                 // 4
		"  const intl = useIntl()",               // 5  match
		"  return <View ref={ref}>{item}</View>", // 6
		"})",                                     // 7
	}
	got := buildSnippetRanges(lines, []int{5}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split generic forwardRef: got %v, want whole component %v", got, want)
	}
}

func TestSnippetMultilineObjectReturnTypeArrow(t *testing.T) {
	lines := []string{
		"export const useControls = (", // 1
		"  location: string,",          // 2
		"): {",                         // 3
		"  ref: (node: HTMLElement | null) => void", // 4  type arrow
		"  controls: Control[]",                     // 5
		"} => {",                                    // 6  implementation arrow
		"  const value = useMemo(() => build(location), [location])", // 7  match
		"  return { controls: value, ref: setRef }",                  // 8
		"}", // 9
	}
	got := buildSnippetRanges(lines, []int{7}, profileForExt(".tsx"))
	want := []SnippetRange{{Start: 1, End: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("multiline object return type: got %v, want whole hook %v", got, want)
	}
}

func TestSnippetCallBindingDoesNotCaptureLaterArrow(t *testing.T) {
	lines := []string{
		"function setup() {",          // 1
		"  const value = loadValue()", // 2  call binding, not an arrow declaration
		"  const auth = useAuth()",    // 3  match
		"",                            // 4
		"  const callback = () => {",  // 5
		"    consume(value)",          // 6
		"  }",                         // 7
		"}",                           // 8
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".ts"))
	want := []SnippetRange{{Start: 1, End: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call binding before arrow: got %v, want outer function %v", got, want)
	}
}

// TestSnippetArrayReturnTypeMethod pins the Spring Framework finding: a Java
// method returning an array (`String[]`) must be recognized; the `]` indexing
// guard applies only to non-empty brackets (`arr[i](...)`).
func TestSnippetArrayReturnTypeMethod(t *testing.T) {
	lines := []string{
		"public class Factory {",                          // 1
		"	private String[] doGetBeanNames(String type) {", // 2
		"		return resolve(type);",                         // 3  match
		"	}",                                              // 4
		"	void other() {",                                 // 5
		"	}",                                              // 6
		"}",                                               // 7
	}
	got := buildSnippetRanges(lines, []int{3}, profileForExt(".java"))
	want := []SnippetRange{{Start: 2, End: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("array return type: got %v, want the method %v", got, want)
	}
}
