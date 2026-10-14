// Package layout describes the on-disk shape wt manages:
//
//	<container>/            plain folder, named after the repository
//	  main/                 the clone; stays on the default branch, never edited
//	  worktrees/<slug>/     one linked worktree per task
//	  wt.links              paths symlinked from main/ into every worktree
//	  wt.conf               per-repository settings, e.g. which IDE to open
package layout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/martbock/wt/internal/gitx"
)

const (
	MainDir      = "main"
	WorktreesDir = "worktrees"
	LinksFile    = "wt.links"
	ConfigFile   = "wt.conf"
)

// ErrNotManaged is returned when a directory is not inside a <container>/main clone.
var ErrNotManaged = errors.New("not a wt-managed clone")

var (
	slugRE   = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	ticketRE = regexp.MustCompile(`^[A-Z][A-Z0-9]*-[0-9]+$`)

	// prefixRE is deliberately narrower than what git allows; the final branch name is checked by git.
	prefixRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	nonSlugRE = regexp.MustCompile(`[^a-z0-9]+`)
)

const maxSlugLen = 60

// Layout holds the absolute paths of one managed repository.
type Layout struct {
	Container string
	Main      string
	Worktrees string

	// Repo is the repository name, taken from the container directory.
	Repo string
}

// FromMain builds the layout around a main clone directory.
func FromMain(main string) Layout {
	container := filepath.Dir(main)

	return Layout{
		Container: container,
		Main:      main,
		Worktrees: filepath.Join(container, WorktreesDir),
		Repo:      filepath.Base(container),
	}
}

// FromContainer builds the layout around a container directory.
func FromContainer(container string) Layout {
	return FromMain(filepath.Join(container, MainDir))
}

// Discover finds the layout that dir belongs to. It works from the main clone, from any of its
// linked worktrees, and from the container folder or worktrees/ folder, which are not repos.
func Discover(ctx context.Context, dir string) (Layout, error) {
	common, err := gitx.Run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if l, ok := findContainer(dir); ok {
			return l, nil
		}

		return Layout{}, fmt.Errorf("%w: %s is not inside a git repository or a <container>/%s clone "+
			"(create one with 'wt clone')", ErrNotManaged, dir, MainDir)
	}

	main := filepath.Dir(common)
	if filepath.Base(common) != ".git" || filepath.Base(main) != MainDir {
		return Layout{}, fmt.Errorf("%w: expected <container>/%s, found %s (create one with 'wt clone')",
			ErrNotManaged, MainDir, main)
	}

	return FromMain(main), nil
}

// findContainer walks up from dir to a folder that holds a main/ git clone.
func findContainer(dir string) (Layout, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Layout{}, false
	}

	// git reports real paths, so resolve symlinks (macOS /tmp, /var) to keep layouts comparable.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}

	for ; ; dir = filepath.Dir(dir) {
		if info, err := os.Stat(filepath.Join(dir, MainDir, ".git")); err == nil && info.IsDir() {
			return FromContainer(dir), true
		}

		if filepath.Dir(dir) == dir {
			return Layout{}, false
		}
	}
}

// WorktreePath is where the worktree for slug lives.
func (l Layout) WorktreePath(slug string) string {
	return filepath.Join(l.Worktrees, slug)
}

// SlugOf returns the slug of a path that is exactly a worktree directory.
func (l Layout) SlugOf(path string) (string, bool) {
	rel, err := filepath.Rel(l.Worktrees, path)
	if err != nil || rel == "." || strings.ContainsRune(rel, filepath.Separator) || strings.HasPrefix(rel, "..") {
		return "", false
	}

	return rel, true
}

// ValidateSlug checks that slug is a short kebab-case name.
func ValidateSlug(slug string) error {
	if len(slug) > maxSlugLen || !slugRE.MatchString(slug) {
		return fmt.Errorf("invalid slug %q: use lowercase kebab-case, at most %d characters", slug, maxSlugLen)
	}

	return nil
}

// IsTicket reports whether s looks like a Jira ticket ID such as PROJ-123.
func IsTicket(s string) bool {
	return ticketRE.MatchString(s)
}

// Branch names the branch for a new worktree: the normalized prefix followed by the slug. A
// ticket ID is never made up.
func Branch(prefix, slug string) string {
	return prefix + slug
}

// NormalizePrefix checks a branch name prefix such as "fix/" or "PROJ-1234-". It is used verbatim,
// except that a bare ticket ID gets the dash that joins it to the slug.
func NormalizePrefix(prefix string) (string, error) {
	switch {
	case prefix == "":
		return "", nil
	case IsTicket(prefix):
		return prefix + "-", nil
	case !prefixRE.MatchString(prefix) || strings.Contains(prefix, ".."):
		return "", fmt.Errorf("invalid prefix %q: use letters, digits, '.', '_', '-' and '/', e.g. fix/ or PROJ-123-", prefix)
	}

	return prefix, nil
}

// SlugFromBranch derives a worktree directory name from a branch name, so that
// "feat/PROJ-1234-Login" becomes "feat-proj-1234-login".
func SlugFromBranch(branch string) (string, error) {
	slug := strings.Trim(nonSlugRE.ReplaceAllString(strings.ToLower(branch), "-"), "-")
	if len(slug) > maxSlugLen {
		slug = strings.TrimRight(slug[:maxSlugLen], "-")
	}

	if err := ValidateSlug(slug); err != nil {
		return "", fmt.Errorf("cannot derive a slug from branch %q, pass one explicitly: %w", branch, err)
	}

	return slug, nil
}
