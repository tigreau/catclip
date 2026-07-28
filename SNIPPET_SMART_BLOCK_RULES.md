# Snippet Smart Block Rules

This is the current reference for how catclip chooses smart blocks for
`--snippet REGEX`. It is written for users who want to understand the output and
for contributors or agents changing the snippet engine.

The implementation lives in:

- `internal/output/snippet_resolution.go`
- `internal/output/snippet_structured.go`

Last checked against the implementation and the Spring Framework, Next.js, and
VS Code corpora on 2026-07-18.

## The simple model

Smart snippet processing has two steps:

1. PCRE2 finds every line matching the user's regex.
2. Catclip finds the smallest useful block surrounding each matching line.

For example:

```bash
catclip src --snippet '\buseAuth\b'
```

PCRE2 decides where `useAuth` matches. Catclip then returns the enclosing
function, component, object, markup element, configuration section, or another
documented block.

The smart-block engine does not decide whether a match is a definition, usage,
comment, string, or example. Those are all valid when the regex selects them.
Use a more precise PCRE2 expression when the match itself is too broad:

```bash
catclip . --snippet '\bdoGetBean\b'
```

Without the word boundaries, `doGetBean` can also match a longer name such as
`doGetBeanNamesForType`. That is regex behavior, not a smart-block error.

## How a smart block is selected

- Catclip chooses a file strategy from the file extension.
- It scans the file once for recognized multi-line units.
- For each matching line, it chooses the smallest recognized unit containing
  that line.
- When equal-sized units contain the match, the later start wins. This normally
  selects the deeper nested unit.
- If no recognized unit contains the match, Catclip uses the file strategy's
  fallback.
- Overlapping and directly adjacent output ranges are merged.
- Every PCRE2 match must remain inside an emitted range.

A method inside a class should therefore beat the class. A nested JSON object
should beat the outer object. A single-line XML leaf can resolve to its nearest
multi-line parent.

## What counts as correct output

- A complete smallest recognized block is correct even when the block is large.
- A paragraph fallback is correct when the syntax has no supported structural
  rule.
- A match in a comment, string, usage, or code example must not be removed.
- Multiple matching callers or declarations are expected when the regex selects
  all of them.
- A recognized block ending before its real closer is a boundary bug.
- A range beginning with detached metadata such as `*/` is a boundary bug.
- Losing a line that PCRE2 matched is always a bug.

## File strategies

| Strategy | Files | Smart-block fallback |
| --- | --- | --- |
| Code with `//` comments | Go, Java, C#, C/C++, Objective-C, Rust, Kotlin, Swift, Scala, TS/JS, Dart, Zig, D, Groovy, Gradle | blank-line paragraph |
| Code with `#` comments | Python, shell, YAML | blank-line paragraph |
| PHP code | `.php`, with `//` and `#` comments | blank-line paragraph |
| SQL-like code | `.sql`, with `--` comments | blank-line paragraph |
| End-delimited code | Ruby, Rake, Gemspec, Elixir | blank-line paragraph |
| JSON structure | `.json`, `.jsonc` | blank-line paragraph |
| Tag structure | XML, HTML, XHTML, XSD, XSL/XSLT, SVG, plist, pom, Vue, Svelte | blank-line paragraph |
| Sections | INI, CFG, gitconfig, TOML | blank-line paragraph |
| Flat configuration | properties, env, editorconfig | match plus two surrounding lines |
| Prose and tabular data | txt, Markdown, RST, AsciiDoc, CSV, TSV, log | blank-line paragraph |
| Default | unknown or extensionless files | code recognition, then paragraph |

## Shared fallback rules

### Blank-line paragraph

- The paragraph contains the matching line and continues until an empty line on
  each side.
- LF empty lines and CRLF blank lines are both boundaries.
- A line containing spaces or tabs is not considered empty. This preserves the
  historical behavior.
- The paragraph is a preservation fallback, not a claim that the text is a
  semantic paragraph.

### Single-line leaves

- Structural candidate lists normally omit single-line leaves.
- This lets a useful multi-line parent win for a match in a one-line member,
  element, or value.
- When no multi-line parent exists, the profile fallback keeps the match.

### Merging

- Repeated matches in the same block emit that block once.
- Nested or overlapping chosen blocks merge.
- Directly adjacent ranges also merge.
- Merging can reduce the number of blocks but cannot remove a matched line.

## Code: functions and methods

- Recognizes keyword functions such as `func`, `function`, `def`, and
  `async function`.
- Recognizes conventional typed methods by finding a method name before `(`.
- Rejects control-flow starts such as `if`, `for`, `return`, `throw`, `new`,
  `await`, and `switch`.
- Rejects member calls and indexed calls so ordinary calls do not become method
  declarations.
- Accepts array return types such as `String[] names(...)`.
- Scans at most 60 lines for the declaration body opener.
- Includes complete one-line bodies.
- Multi-line code bodies currently end by indentation and a conventional closing
  line, not by full language parsing.
- A body match normally returns the function or method instead of its enclosing
  class.

## Code: classes, interfaces, enums, records, and related types

- Recognizes `class`, `enum`, `interface`, `record`, `struct`, and `trait`.
- Language profiles add common forms such as Rust `impl`, `mod`, and
  `macro_rules!`; C# `namespace`; Kotlin `object`; and Swift `protocol`,
  `extension`, and `actor`.
- A match inside an enum member normally returns the enum unless a smaller
  recognized nested declaration owns the line.
- An interface method without a body normally belongs to the interface.
- Multiline type declarations use the same bounded header and indentation rules
  as functions.

## Code: Java annotations

- A Java annotation on a line above a declaration is attached to that
  declaration.
- Wrapped annotation arguments are balanced across lines.
- A same-line return annotation is separated from the declaration for
  classification:

  ```java
  @Nullable Object resolveAutowiredArgument(...) {
      // ...
  }
  ```

- Package-private and modifier-bearing methods both work.
- Annotation strings and nested parentheses do not end the annotation early.
- The emitted range still begins on the original annotation line.

## Code: JavaScript and TypeScript function bindings

- Recognizes named `const`, `let`, and `var` bindings assigned to a block-bodied
  arrow or function expression.
- Recognizes a leading `export`.
- Supports parenthesis-free arrows such as `const App = props => {`.
- Supports generic arrows and multiline generic headers.
- Supports `React.FC<...>` and `React.FC<{...}>` headers.
- Supports common `memo(...)` and `forwardRef(...)` wrappers.
- Supports multiline destructured parameters and named function callbacks.
- Tracks the binding assignment separately from arrows in TypeScript types.
- A type member such as `onClick: () => void` is not an implementation arrow.
- Comment-only rows and structurally open blank rows may remain inside the
  bounded header scan.
- A redundant function candidate inside a wrapper header is removed so the
  complete outer binding owns the wrapper and callback.
- A genuine nested function beginning inside the body remains its own candidate.

## Code: JavaScript and TypeScript object and array bindings

Catclip recognizes these named assignments:

```text
[export] const|let|var NAME = { ... }
[export] const|let|var NAME = [ ... ]
```

- The assigned object or array is balanced through its real closing delimiter.
- Nested objects and arrays remain inside the outer binding.
- Internal blank lines do not stop the block.
- Braces and brackets inside strings or comments do not count.
- A trailing semicolon is included, including a semicolon on the following line.
- An attached declaration comment is included.
- A local object or array can be the smallest block inside a larger function.
- Destructured left sides are deliberately not recognized by this rule.
- Typed assignments such as `const options: Config = { ... }` are deliberately
  not recognized by this narrow rule.
- A split typed-object declaration such as `const options: {` must never become
  a false function-binding candidate.

## Code: decorators, attributes, and declaration comments

- Contiguous decorators, attributes, annotations, and declaration comments
  immediately above a recognized declaration are included.
- Supported prefix shapes include `@decorator`, `[Attribute]`, `//`, `///`,
  `#` comment forms, and `/* ... */` blocks.
- Wrapped decorator or annotation arguments are balanced.
- Attachment scans at most 40 lines upward.
- A blank line or unrelated statement stops attachment.
- A contiguous block comment is attached as one complete unit or not at all.
- A result must never begin with a detached `*/`.
- A standalone comment remains a valid regex match and uses its enclosing unit
  or fallback.

## Code: imports and other leaf statements

- Imports are not separate structural units.
- A top-level import match normally returns its blank-line import group.
- A one-line constant, type alias, field, or method signature may use fallback
  unless a recognized multi-line owner contains it.
- Catclip only claims structural ownership when it has a defensible stopping
  rule.

## Code: shapes that use fallback

- Expression-bodied bindings such as `const App = props => <View />` do not own
  a brace-delimited function block.
- Bindings that directly return another call, such as
  `const useThing = () => useCallback(...)`, use fallback.
- Generic callback-bearing call statements are not structural units. A match in
  `registerCommand(..., () => { ... });` can therefore return its paragraph or a
  smaller recognized declaration containing it.
- Arbitrary object properties, tests, calls, and examples are not reclassified
  as declarations.

## End-delimited code: Ruby and Elixir

- Ruby starts include `class`, `module`, and `def` with visibility prefixes.
- Elixir starts include modules, protocols, implementations, definitions,
  macros, guards, and common test blocks.
- The block follows blank or more-indented lines and reattaches `end` at the
  declaration's indentation.
- Nested declarations participate in smallest-block selection.
- This relies on conventional formatting rather than a Ruby or Elixir parser.

## Markup: XML

- Every paired multi-line `<tag>...</tag>` element can be a block.
- A match in a single-line leaf returns its nearest multi-line ancestor.
- Tag names are case-sensitive.
- XML self-closing tags do not open blocks.
- Attribute quotes, comments, CDATA, processing instructions, and declarations
  are ignored while finding tag boundaries.
- Multiline opening tags keep their real first line.
- Malformed markup produces fewer candidates and falls back safely.

## Markup: HTML

- Native tag names are case-insensitive.
- Void elements such as `img`, `input`, `br`, and `meta` never open blocks.
- Common optional-close rules are modeled for list items, paragraphs, table
  rows/cells, options, and table sections.
- An appropriate sibling or ancestor can close an optional element.
- `script` and `style` contents are raw text, so tag-like strings inside
  JavaScript or CSS do not change the HTML stack.
- This is a conservative boundary model, not a complete HTML5 parser.

## Markup: Vue and Svelte

- The whole file uses tag recognition.
- A match inside `<script>` normally returns the complete script element rather
  than a JavaScript function inside it.
- Lowercase native tags use HTML void and optional-close behavior.
- Component names remain case-sensitive so PascalCase components are not
  confused with native elements.
- Script, style, and markup regions are not parsed by separate language engines.

## Data: JSON and JSONC

- Every multi-line object and array is a candidate.
- A nested key or value returns its smallest enclosing object or array.
- Strings and escaped quotes are tracked.
- JSONC line and block comments are ignored while balancing.
- Single-line objects and arrays are omitted as leaves.
- Mismatched or unbalanced delimiters produce fewer candidates and fall back
  instead of returning a false block.

## Configuration: INI, CFG, gitconfig, and TOML

- A trimmed line beginning with `[` and ending with `]` starts a section.
- The section ends before the next header or at end of file.
- A match inside a section returns the complete section.
- Nested TOML table meaning is not interpreted; headers are flat boundaries.

## Configuration: properties, env, and editorconfig

- These formats do not create structural units.
- Smart mode returns the matching line with up to two lines on each side.
- The window is clamped to the file.

## Prose and tabular data

- Plain text, Markdown, RST, AsciiDoc, CSV, TSV, and logs always use the
  blank-line paragraph.
- Code-like text is not treated as a declaration.
- Markdown headings, fenced blocks, CSV records, and log events are not separate
  smart blocks.

## Unknown extensions

- Unknown and extensionless files use the generic code recognizer with `//`
  comment handling.
- This preserves useful behavior for source formats without a dedicated profile.
- If no declaration is recognized, the paragraph fallback keeps the match.

## Current limitations

- Code body extent is indentation-based. Unindented bodies and column-zero
  preprocessor lines can defeat recognition.
- YAML mappings and sequences do not have indentation-aware blocks.
- Vue and Svelte do not switch to a code engine inside script regions.
- JSX elements are not independent blocks; their enclosing component normally
  owns the match.
- Expression-bodied functions and generic callback call statements use fallback.
- Imports and other single-line leaf declarations do not have dedicated units.
- Minified one-line files cannot be divided into useful line-based blocks.
- Malformed structured input deliberately loses candidates rather than inventing
  a boundary.

## Probe checklist for audits

Use all of these probe types when changing the engine. One broad identifier
query is not enough.

### Definitions

- Query exact names where practical.
- Verify body matches return the complete smallest declaration.
- Usages selected by the same regex remain valid output.

### Usages and callers

- Probe calls inside functions, methods, classes, and top-level statements.
- Verify every match survives and the chosen block has a documented boundary.
- Multiple callers are expected when the regex selects them.

### Line comments

- Probe comments in headers, bodies, enums, and standalone paragraphs.
- Punctuation inside comments must not alter header or delimiter state.

### Block comments and JSDoc

- Probe attached comments and comments embedded in headers.
- Verify attached comments are complete.
- Include a separated earlier comment as a negative attachment case.

### Strings and code examples

- Put braces, arrows, parentheses, tags, and matched identifiers inside strings.
- The match must remain, but punctuation inside the string must not create or
  close a block.

### Imports

- Probe single imports and blank-line-separated import groups.
- Verify the documented paragraph behavior.

### Functions and methods

- Cover keyword functions, typed methods, multiline parameters, array return
  types, annotations, and one-line bodies.
- Include nested functions to verify smallest-block selection.

### Classes, interfaces, records, and traits

- Probe declaration lines and interior members.
- A complete large owner is a pass when no smaller supported unit owns the line.

### Enums and enum members

- Probe declarations, members, comments between members, and nested enums.
- Verify a smaller nested declaration wins.

### Type aliases and signatures

- Probe single-line and multiline aliases and interface signatures.
- Leaf shapes must not create false body declarations.

### Decorators, annotations, and attributes

- Probe single-line and multiline adornments, arguments, comments in arguments,
  same-line Java return annotations, and unrelated preceding statements.
- Verify attachment remains contiguous and bounded.

### Function bindings

- Cover exported, generic, parenthesis-free, `React.FC`, `memo`, and
  `forwardRef` forms.
- Add prop-type arrows, typed object returns, comments, blank lines, and strings
  as false-positive pressure.

### Object and array bindings

- Cover nested literals, internal blank lines, delimiter text in strings and
  comments, trailing semicolons, local bindings, and attached comments.
- Keep destructured and typed left sides as negative cases.

### JSX and markup

- Probe component bodies, JSX text and attributes, native HTML, custom
  components, raw script/style text, and optional-close HTML.
- Verify the file profile, not the matched text, chooses code or tag behavior.

### Callback-bearing statements

- Probe multiline calls with callbacks and object arguments.
- Verify they use a documented enclosing declaration or fallback rather than a
  false statement boundary.

### Fallbacks

- Cover unsupported syntax, malformed structured input, prose, flat config,
  unknown extensions, CRLF files, and whitespace-only separator lines.
- Every matched line must survive.

## Performance rules for contributors

- Candidate discovery must remain linear or bounded per file.
- Code header scans are capped at 60 lines.
- Declaration attachment scans are capped at 40 lines.
- Structured balancing scans a file once.
- Do not rescan the remainder of a file independently for every match.
- Candidate generation must not invoke ripgrep again or rediscover the project.
- Normal smart-block resolution does not invoke an AST runtime, Node,
  TypeScript, or another language process.
- New block types need focused benchmarks and a large real-corpus timing.

## Rules for changing the engine

Before changing a smart-block rule, answer these questions:

1. Which file profiles and probe types change?
2. Does the proposal change what PCRE2 matches? If so, it does not belong in the
   smart-block engine.
3. What exact syntax authorizes the new block?
4. What exact condition ends it?
5. What strings, comments, nested syntax, and malformed inputs can imitate it?
6. Can it steal a match from an existing smaller block?
7. What happens when recognition fails halfway through?
8. Does the documented fallback still preserve the match?
9. What is the time and allocation impact on a large corpus?
10. Which positive, negative, match-preservation, and real-corpus tests pin the
    decision?

When a rule changes, update this file, implementation tests, corpus evidence,
and release notes together. Do not leave future or proposed behavior in this
current reference.
