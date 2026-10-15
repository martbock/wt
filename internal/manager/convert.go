package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/martbock/wt/internal/gitx"
	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/links"
)

// ConvertOptions describes an existing clone to bring into the wt layout.
type ConvertOptions struct {
	// Dir is the clone: a directory with a .git directory directly in it.
	Dir string

	// Links are the paths, relative to main/, that worktrees symlink back to main/. They are
	// written to wt.yml. Empty links nothing.
	Links []string

	// IDE is written to wt.yml as this repository's IDE. Empty leaves the choice to $WT_IDE_APP
	// and the global setting.
	IDE string
}

// Converted describes a repository that is now managed.
type Converted struct {
	Layout layout.Layout
	Init   InitResult

	// Branch is what main/ has checked out, empty when detached. Dirty reports uncommitted
	// changes. wt expects main/ to sit on the default branch and stay clean, but converting
	// changes neither.
	Branch string
	Dirty  bool

	// DefaultBranch is the branch Branch is expected to be.
	DefaultBranch string
}

// Convert turns an existing clone into <dir>/main, so the directory becomes the container and the
// repository keeps its name and place. Nothing inside the clone is touched, so uncommitted work,
// stashes and ignored files such as local config come along.
func Convert(ctx context.Context, opts ConvertOptions) (Converted, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Converted{}, err
	}

	// Reject bad input before anything is moved.
	entries, err := links.ParseEntries(opts.Links)
	if err != nil {
		return Converted{}, err
	}
	if err := checkConvertible(ctx, dir); err != nil {
		return Converted{}, err
	}

	l := layout.FromContainer(dir)
	if err := moveIntoMain(dir, l.Main); err != nil {
		return Converted{}, err
	}

	res := Converted{Layout: l}
	m := Manager{Layout: l}

	if res.Init, err = m.setUp(ctx, opts.IDE, entries); err != nil {
		return res, fmt.Errorf("moved the clone to %s, but %w", l.Main, err)
	}

	res.Branch, _ = gitx.Run(ctx, l.Main, "branch", "--show-current")
	res.DefaultBranch = strings.TrimPrefix(m.DefaultBase(ctx), "origin/")
	if status, err := gitx.Run(ctx, l.Main, "status", "--porcelain"); err == nil {
		res.Dirty = status != ""
	}

	return res, nil
}

// checkConvertible refuses anything but a plain clone that nothing else depends on.
func checkConvertible(ctx context.Context, dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory (a symlink to one? pass the real path)", dir)
	}

	if filepath.Base(dir) == layout.MainDir {
		return fmt.Errorf("%s is called %s, so it is a <container>/%s clone already: run 'wt init' in it",
			dir, layout.MainDir, layout.MainDir)
	}

	git, err := os.Lstat(filepath.Join(dir, ".git"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s has no .git directory: pass the root of a clone, not a subdirectory or a bare repository", dir)
	case err != nil:
		return err
	case !git.IsDir():
		return fmt.Errorf("%s is a linked worktree or submodule, not a clone: convert the repository it belongs to", dir)
	}

	// A linked worktree records the path of its repository, so moving the clone would break it.
	out, err := gitx.Run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	linked := -1 // the first entry is the clone itself
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			linked++
		}
	}
	if linked > 0 {
		return fmt.Errorf("%s has %d linked worktrees, which would lose track of it: remove them with "+
			"'git worktree remove' first", dir, linked)
	}

	return nil
}

// moveIntoMain turns dir into the container of main by renaming rather than moving entry by entry,
// so every name inside the clone, even a main/ or worktrees/ of its own, stays as it is.
func moveIntoMain(dir, main string) error {
	// Pick an unused name next to dir. os.Rename will not replace a directory, empty or not, so
	// the reservation is dropped again before it, and a collision in between fails the rename.
	parking, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+"-convert-")
	if err != nil {
		return err
	}
	if err := os.Remove(parking); err != nil {
		return err
	}

	if err := os.Rename(dir, parking); err != nil {
		return err
	}

	restore := func(cause error) error {
		if err := os.Rename(parking, dir); err != nil {
			return fmt.Errorf("%w; the clone is left at %s, move it back to %s", cause, parking, dir)
		}

		return cause
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		return restore(err)
	}
	if err := os.Rename(parking, main); err != nil {
		_ = os.Remove(dir)
		return restore(err)
	}

	return nil
}
