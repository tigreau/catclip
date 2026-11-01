package git

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// TrackedPaths checks literal identities in bounded argv batches. NUL records
// preserve whitespace/newlines and avoid Git's quoted display-path format.
func TrackedPaths(ctx Context, paths []string) (map[string]bool, error) {
	tracked := make(map[string]bool, len(paths))
	for _, batch := range gitPathBatches(paths) {
		args := append([]string{"--literal-pathspecs", "ls-files", "-z", "--"}, batch...)
		out, err := Capture(ctx.Root, args...)
		if err != nil {
			return nil, err
		}
		for _, name := range strings.Split(out, "\x00") {
			if name != "" {
				tracked[name] = true
			}
		}
	}
	return tracked, nil
}

// CanBatchPatches avoids invoking custom diff programs an extra time just to
// discover that their output cannot be partitioned. All such comparisons keep
// the single-path implementation. No configuration is changed.
func CanBatchPatches(ctx Context, paths []string) (bool, error) {
	if _, set := os.LookupEnv("GIT_EXTERNAL_DIFF"); set {
		return false, nil
	}
	out, err := Capture(ctx.Root, "config", "--get-regexp", `^diff\.(external|.*\.(command|textconv))$`)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	if strings.TrimSpace(out) == "" {
		return true, nil
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "diff.external" {
			return false, nil
		}
	}
	// Git for Windows ships an astextplain driver even when no selected file
	// uses it. Check attributes only when a configured driver could be active.
	for _, batch := range gitPathBatches(paths) {
		args := append([]string{"check-attr", "-z", "diff", "--"}, batch...)
		attrs, err := Capture(ctx.Root, args...)
		if err != nil {
			return false, err
		}
		fields := strings.Split(strings.TrimSuffix(attrs, "\x00"), "\x00")
		if len(fields)%3 != 0 {
			return false, nil
		}
		for i := 2; i < len(fields); i += 3 {
			if fields[i] != "unspecified" && fields[i] != "set" && fields[i] != "unset" {
				return false, nil
			}
		}
	}
	return true, nil
}

// BatchModifiedPatches returns only comparisons whose raw records prove simple
// same-path modifications and whose patch boundaries are unambiguous. Anything
// else (renames, additions/deletions, custom prefixes, unusual names, conflicts)
// is absent from the map and must use the original per-path comparison.
func BatchModifiedPatches(ctx Context, paths []string, staged, unstaged bool) (map[string]string, error) {
	if !staged && !unstaged || staged && unstaged {
		if !ctx.HasHead {
			return nil, nil // preserve staged + blank line + unstaged on unborn HEAD
		}
	}
	eligible := make([]string, 0, len(paths))
	for _, name := range paths {
		if simplePatchPath(name) {
			eligible = append(eligible, name)
		}
	}
	result := make(map[string]string)
	for _, batch := range gitPathBatches(eligible) {
		if len(batch) < 2 {
			continue
		}
		args := []string{"--literal-pathspecs", "diff", "--raw", "-z", "--patch"}
		switch {
		case staged && !unstaged:
			args = append(args, "--cached")
		case unstaged && !staged:
		default:
			args = append(args, "HEAD")
		}
		args = append(args, "--")
		args = append(args, batch...)
		out, err := Capture(ctx.Root, args...)
		if err != nil {
			return nil, err
		}
		if patches, ok := splitModifiedPatches(out, batch); ok {
			for name, patch := range patches {
				result[name] = patch
			}
		}
	}
	return result, nil
}

func simplePatchPath(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._/-", c)) {
			return false
		}
	}
	return true
}

func splitModifiedPatches(out string, requested []string) (map[string]string, bool) {
	patches := make(map[string]string, len(requested))
	for _, name := range requested {
		patches[name] = ""
	}
	if out == "" {
		return patches, true
	}
	boundary := strings.Index(out, "\x00\x00")
	if boundary < 0 {
		return nil, false
	}
	raw := strings.Split(out[:boundary], "\x00")
	if len(raw)%2 != 0 {
		return nil, false
	}
	names := make([]string, 0, len(raw)/2)
	seen := make(map[string]bool)
	for i := 0; i < len(raw); i += 2 {
		fields := strings.Fields(raw[i])
		name := raw[i+1]
		if len(fields) != 5 || !strings.HasPrefix(fields[0], ":") || fields[4] != "M" || seen[name] {
			return nil, false
		}
		if _, ok := patches[name]; !ok {
			return nil, false
		}
		seen[name] = true
		names = append(names, name)
	}
	body := out[boundary+2:]
	starts := []int{0}
	for offset := 0; ; {
		i := strings.Index(body[offset:], "\ndiff --git ")
		if i < 0 {
			break
		}
		offset += i + 1
		starts = append(starts, offset)
	}
	if len(starts) != len(names) {
		return nil, false
	}
	starts = append(starts, len(body))
	for i, name := range names {
		patch := body[starts[i]:starts[i+1]]
		if !strings.HasPrefix(patch, "diff --git a/"+name+" b/"+name+"\n") {
			return nil, false
		}
		patches[name] = patch
	}
	return patches, true
}

func gitPathBatches(paths []string) [][]string {
	const maxBytes, maxPaths = 12000, 100
	var batches [][]string
	for start := 0; start < len(paths); {
		end, size := start, 0
		for end < len(paths) && end-start < maxPaths {
			cost := 2*len(paths[end]) + 3 // conservative quoting/UTF-16 budget
			if end > start && size+cost > maxBytes {
				break
			}
			size += cost
			end++
		}
		batches = append(batches, paths[start:end])
		start = end
	}
	return batches
}
