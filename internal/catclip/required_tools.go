package catclip

import (
	"fmt"
	"io"
	"strings"

	"github.com/tigreau/catclip/internal/discovery"
	"github.com/tigreau/catclip/internal/search"
)

func ensureRequiredTools(stderr io.Writer) error {
	var missing []string
	if _, ok := search.RipgrepBinary(); !ok {
		missing = append(missing, "ripgrep (rg)")
	}
	if _, ok := discovery.FzfBinary(); !ok {
		missing = append(missing, "fzf")
	}
	if len(missing) == 0 {
		return nil
	}
	fmt.Fprintf(stderr, "catclip v%s\n\n", loadVersion())
	fmt.Fprintf(stderr, "Error: missing required dependencies: %s\n\n", strings.Join(missing, ", "))
	fmt.Fprintf(stderr, "  catclip ships with bundled copies of ripgrep and fzf.\n")
	fmt.Fprintf(stderr, "  If they are missing, your installation is incomplete.\n\n")
	fmt.Fprintf(stderr, "  Reinstall:\n")
	fmt.Fprintf(stderr, "    Homebrew:  brew reinstall catclip\n")
	fmt.Fprintf(stderr, "    Script:    curl -fsSL https://raw.githubusercontent.com/tigreau/catclip/main/install.sh | bash\n")
	fmt.Fprintf(stderr, "    Source:    ./install.sh\n")
	fmt.Fprintf(stderr, "    Dev:       make dev\n")
	return fmt.Errorf("missing required dependencies")
}
