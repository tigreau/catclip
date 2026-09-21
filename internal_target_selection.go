package catclip

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/picker"
)

func readTargetSelection(selectionPath string) ([]string, error) {
	f, err := os.Open(picker.SelectionFilePath(selectionPath))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var targets []string
	seen := make(map[string]struct{})
	all := false
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 4 || fields[1] == "" || (fields[2] != "file" && fields[2] != "dir") {
			return nil, fmt.Errorf("invalid fzf target selection row")
		}
		target := fields[1]
		if err := discovery.ValidateTargetBoundary(target); err != nil {
			return nil, err
		}
		if target == "." {
			if fields[2] != "dir" {
				return nil, fmt.Errorf("invalid fzf all-target selection row")
			}
			all = true
		}
		if _, ok := seen[target]; !ok {
			seen[target] = struct{}{}
			targets = append(targets, target)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("empty fzf target selection")
	}
	if all {
		return []string{"."}, nil
	}
	return targets, nil
}
