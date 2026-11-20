package cli

// --unpack-reply-format copies this text: how a chat agent replies so
// --unpack can apply it, how it asks for more code with catclip commands, and
// what the tags mean. Built into catclip so it works offline. Each user also
// gets an editable copy in catclip's config folder (UnpackReplyFormatFile);
// when it differs from this text, --unpack-reply-format asks whether to
// replace it (user, 2026-11-19).

// UnpackReplyFormatFile is the copy's name in catclip's config folder,
// next to .hiss.
const UnpackReplyFormatFile = "unpack-reply-format-for-agents.md"

// UnpackReplyFormatDefault is the built-in text, ordered by what breaks an
// unpack: built from real chat replies and web-chat tests with Sonnet and
// Haiku (tabs turned into spaces, copied line numbers, shortened bodies,
// changed tags, guessed and placeholder paths, unknown project layout).
const UnpackReplyFormatDefault = "" +
	"# catclip for web agents\n" +
	"\n" +
	"I share my code with you using catclip, and I apply your changes to my\n" +
	"project with `catclip --unpack`, a tool that reads the exact format below.\n" +
	"Follow it whenever you change code.\n" +
	"\n" +
	"## 1. How to reply\n" +
	"\n" +
	"1. Put ALL changed files in ONE code block. Write explanations before or after\n" +
	"   the block, never inside it. If a file contains ```, open and close the block\n" +
	"   with ```` instead.\n" +
	"2. Inside the block, write one entry per file: a line `<file path=\"...\">`\n" +
	"   (the same tag I sent you), the code, and a line `</file>`. Write the tags as\n" +
	"   plain text: never escape `<` `>` as `&lt;` `&gt;`, and never put a line that\n" +
	"   is only `</file>` inside the code.\n" +
	"3. Always write the COMPLETE code for each entry. Never shorten it with `...`,\n" +
	"   `// rest unchanged`, `// existing code` or similar: anything you leave out is\n" +
	"   deleted.\n" +
	"4. Keep the code exactly as I wrote it except for your changes: the same\n" +
	"   indentation (tabs stay tabs, spaces stay spaces), blank lines and style. You\n" +
	"   may reformat code you change if it still compiles and runs. Never include\n" +
	"   line numbers.\n" +
	"\n" +
	"Three cases:\n" +
	"\n" +
	"A) Change a whole file, or create a new one: a tag with only `path=`, and the\n" +
	"   complete new file as the body. A new file never gets `lines=` or `out-of=`.\n" +
	"\n" +
	"```text\n" +
	"<file path=\"internal/handler/health.go\">\n" +
	"package handler\n" +
	"\n" +
	"import \"net/http\"\n" +
	"\n" +
	"func Health(w http.ResponseWriter, r *http.Request) {\n" +
	"\tw.WriteHeader(http.StatusOK)\n" +
	"}\n" +
	"</file>\n" +
	"```\n" +
	"\n" +
	"B) Change an excerpt I sent. Its tag says where it came from:\n" +
	"   `lines=\"40-60\" out-of=\"210\"` means lines 40 to 60, out of a file that had\n" +
	"   210 lines when I copied it. Copy that tag EXACTLY, never changing its\n" +
	"   numbers, and write the complete new version of the whole excerpt. It may be\n" +
	"   longer or shorter than before. Several excerpts of one file: each keeps its\n" +
	"   own original tag and my original line numbers, and ranges must not overlap.\n" +
	"\n" +
	"```text\n" +
	"<file path=\"src/components/Button.tsx\" lines=\"12-14\" out-of=\"48\">\n" +
	"export function Button({ label, onClick }: ButtonProps) {\n" +
	"  return <button className=\"btn\" onClick={onClick}>{label}</button>\n" +
	"}\n" +
	"</file>\n" +
	"```\n" +
	"\n" +
	"C) Add code at the end of a file tagged `out-of=\"N\"`: use `lines=\"N+1-N+1\"` with\n" +
	"   the same `out-of`. Start with a blank line if you want one before it.\n" +
	"\n" +
	"```text\n" +
	"<file path=\"internal/store/store.go\" lines=\"39-39\" out-of=\"38\">\n" +
	"\n" +
	"func (s *UserStore) Count() int { return len(s.users) }\n" +
	"</file>\n" +
	"```\n" +
	"\n" +
	"How I use your tags: for an excerpt, `catclip --unpack` first checks that my\n" +
	"file still has `out-of` lines. If not, the file changed since I copied it, and\n" +
	"nothing is written. Then it removes lines A to B from my file and puts your\n" +
	"code in their place. The numbers say which of my lines are replaced, not how\n" +
	"long your code is, so never change them: other lines of my file, ones you never\n" +
	"saw, would be deleted.\n" +
	"\n" +
	"## 2. Need more code?\n" +
	"\n" +
	"Do not guess names, signatures, paths or line numbers, and do not edit lines I\n" +
	"did not send you. Reply without code and ask me to run catclip commands; I\n" +
	"paste what they give me. Write each command on its own line so I can copy it.\n" +
	"Several files or folders fit in one command:\n" +
	"\n" +
	"```text\n" +
	"catclip internal/store/store.go cmd/api/main.go\n" +
	"```\n" +
	"\n" +
	"When a part needs other options, give it its own command, for example only\n" +
	"the first 20 lines (the imports) of a file:\n" +
	"\n" +
	"```text\n" +
	"catclip internal/store/store.go\n" +
	"catclip cmd/api/main.go --lines 1 20\n" +
	"```\n" +
	"\n" +
	"Use only paths you have seen. If you do not know where something is, ask for\n" +
	"the file list first (`catclip . --paths`). Otherwise use what you do know: a\n" +
	"folder (`catclip internal/store`, e.g. from an import path) or just a file\n" +
	"name (`catclip store.go`); catclip finds both. Never write a placeholder path.\n" +
	"\n" +
	"Ignored files and folders (build output, generated code, docs) are left out\n" +
	"of every list, but `catclip . --metadata` names them. To see one, ask for it by\n" +
	"its exact path, e.g. `catclip docs/api.md` or `catclip dist`.\n" +
	"\n" +
	"## 3. What my tags mean\n" +
	"\n" +
	"| Tag | Meaning |\n" +
	"| --- | --- |\n" +
	"| `<file path=\"src/lib/api.ts\">` | The complete file. |\n" +
	"| `<file path=\"src/lib/api.ts\" lines=\"40-60\" out-of=\"210\">` | Lines 40-60, out of a file that had 210 lines when I copied it. |\n" +
	"| `<file path=\"src/lib/api.ts\" numbered=\"true\">` | The complete file with line numbers added for reading (not part of the code). |\n" +
	"| `type=\"binary\"`, `type=\"diff\"`, `partial=\"true\"` | A binary file's description, a Git patch, or a file cut short by a size limit. Never send these back. |\n" +
	"\n" +
	"A plain list of paths, one per line, is my project's file list. A table with\n" +
	"SIZE, TOKENS, GIT and MODIFIED columns is the same list with details.\n" +
	"\n" +
	"## 4. Commands you can ask me to run\n" +
	"\n" +
	"| Command | Gives you |\n" +
	"| --- | --- |\n" +
	"| `catclip . --paths` | Every file path in the project |\n" +
	"| `catclip . --metadata` | The file list with sizes, tokens, Git status and dates, and what is ignored |\n" +
	"| `catclip IGNORED-PATH` | An ignored file or folder, named by its exact path |\n" +
	"| `catclip src --paths` | The files under one folder |\n" +
	"| `catclip . --depth 2 --paths` | Files at the root and one folder below |\n" +
	"| `catclip PATH` | A whole file (best for small files) |\n" +
	"| `catclip FOLDER` | Every file in a folder |\n" +
	"| `catclip PATH FOLDER ...` | Several files or folders at once |\n" +
	"| `catclip store.go` | A file found by its name anywhere in the project |\n" +
	"| `catclip PATH --lines A B` | Lines A-B of a file, e.g. the imports at the top |\n" +
	"| `catclip PATH --snippet 'REGEX'` | The function, class or block around each match |\n" +
	"| `catclip PATH --snippet 'REGEX' N` | Each matching line with N lines around it |\n" +
	"| `catclip . --contains 'REGEX' --paths` | The files that mention something |\n" +
	"| `catclip . --only '*.go' --paths` | The files matching a glob |\n" +
	"\n" +
	"Patterns are regular expressions in single quotes: escape `( ) [ ] . * + ?`;\n" +
	"uppercase letters make them case-sensitive.\n" +
	"\n" +
	"## 5. If catclip refuses your reply\n" +
	"\n" +
	"Sometimes I paste a catclip error instead of a confirmation. It names each file\n" +
	"and the problem:\n" +
	"\n" +
	"- A tag's `out-of` no longer matches my file: the file changed since I sent it.\n" +
	"  Wait for the current code I paste, and redo the change with its new tags.\n" +
	"  Never just change the numbers to make it fit.\n" +
	"- Code was shortened (`...`, `// rest unchanged`): send that entry again,\n" +
	"  complete.\n" +
	"\n" +
	"## Never\n" +
	"\n" +
	"- change paths: always relative, forward slashes, no `..`, no absolute paths\n" +
	"- change the numbers in `lines=` or `out-of=` to make a tag fit\n" +
	"- delete files: list files to delete in plain text, outside the block\n" +
	"- send Git patches or diffs instead of entries\n"

// IsUnpackReplyFormatRequest reports whether args ask for
// --unpack-reply-format as a flag, not as the value of another option.
func IsUnpackReplyFormatRequest(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--unpack-reply-format" {
			return true
		}
		i += FixedValueCount(args[i])
	}
	return false
}

// ReplyFormatOptions are the flags --unpack-reply-format accepts.
//   - Print (-p): show the user's copy instead of copying it; never asks.
//   - Headless: for agents. Prints the text with no question and no
//     next-steps for the user; when the user's copy differs from catclip's
//     it says so on stderr and points to --yes.
//   - Yes (-y): take catclip's version without asking (replaces the copy).
//     Not in --help: the headless hint is where agents learn it.
type ReplyFormatOptions struct {
	Print, Headless, Yes bool
}

// ParseUnpackReplyFormatArgs parses --unpack-reply-format and its flags.
// Like --theme it takes no targets.
func ParseUnpackReplyFormatArgs(args []string) (ReplyFormatOptions, error) {
	var opts ReplyFormatOptions
	for _, arg := range args {
		switch arg {
		case "--unpack-reply-format":
		case "-p", "--print":
			opts.Print = true
		case "--headless":
			opts.Headless = true
		case "-y", "--yes":
			opts.Yes = true
		default:
			return ReplyFormatOptions{}, newUsageError("Error: --unpack-reply-format cannot be combined with %s.\n"+
				"  catclip --unpack-reply-format           copy the reply format for agents\n"+
				"  catclip --unpack-reply-format --print   show it instead", arg)
		}
	}
	return opts, nil
}
