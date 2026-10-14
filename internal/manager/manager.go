// Package manager holds the worktree operations. It returns plain data and never prints or
// exits; the cli package renders the results.
package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/martbock/wt/internal/gitx"
	"github.com/martbock/wt/internal/layout"
)

// Manager operates on one managed repository.
type Manager struct {
	Layout layout.Layout
}

// DefaultBase is the ref new worktrees branch from: the remote's default branch.
func (m Manager) DefaultBase(ctx context.Context) string {
	ref, err := gitx.Run(ctx, m.Layout.Main, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || ref == "" {
		return "origin/main"
	}

	return ref
}

// Fetch updates the remote-tracking refs and prunes deleted remote branches.
func (m Manager) Fetch(ctx context.Context) error {
	_, err := gitx.Run(ctx, m.Layout.Main, "fetch", "--prune", "origin")
	return err
}

// ensureIgnored keeps paths that wt creates in a checkout out of git's view, in repositories
// whose .gitignore does not already cover them. It appends to the shared info/exclude, which
// is local to this machine and covers all worktrees.
func (m Manager) ensureIgnored(ctx context.Context, checkout string, rels ...string) error {
	var missing []string
	for _, rel := range rels {
		if !gitx.OK(ctx, checkout, "check-ignore", "-q", "--", rel) {
			missing = append(missing, "/"+filepath.ToSlash(rel))
		}
	}
	if len(missing) == 0 {
		return nil
	}

	exclude, err := gitx.Run(ctx, m.Layout.Main, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := strings.Split(string(existing), "\n")
	var add strings.Builder
	for _, line := range missing {
		if !contains(lines, line) {
			add.WriteString(line + "\n")
		}
	}
	if add.Len() == 0 {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}

	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + add.String()); err != nil {
		_ = f.Close()
		return err
	}

	return f.Close()
}

func contains(lines []string, want string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}

	return false
}
