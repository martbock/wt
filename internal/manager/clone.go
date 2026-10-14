package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/martbock/wt/internal/config"
	"github.com/martbock/wt/internal/gitx"
	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/links"
)

// CloneOptions describes a new managed repository.
type CloneOptions struct {
	URL string

	// Dir is the container directory, relative to the current directory like git clone's. Empty
	// means the repository's name.
	Dir string

	// Links are the paths, relative to main/, that worktrees symlink back to main/. They are
	// written to wt.links. Empty links nothing; it can be changed later with AddLinks and
	// RemoveLinks.
	Links []string

	// IDE is written to wt.conf as this repository's IDE: a preset such as "vscode" or a macOS
	// application name. Empty leaves the choice to $WT_IDE_APP and the default.
	IDE string

	// Seed is an existing clone whose gitignored files, the ones selected by Links, are copied
	// into the new main/, so local config does not have to be recreated by hand.
	Seed string
}

// Cloned describes a new managed repository.
type Cloned struct {
	Layout layout.Layout
	Init   InitResult
	Seeded []links.Action
}

var repoRE = regexp.MustCompile(`[/:]([^/:]+?)(?:\.git)?/*$`)

// RepoName extracts the repository name, the last path component without ".git", from a git
// remote URL or path, the way git clone does.
func RepoName(url string) (string, error) {
	m := repoRE.FindStringSubmatch(url)
	if m == nil {
		return "", fmt.Errorf("cannot derive a repository name from %q", url)
	}

	return m[1], nil
}

// Clone clones a repository into <dir>/main, initializes it and lists opts.Links in wt.links.
func Clone(ctx context.Context, opts CloneOptions) (Cloned, error) {
	dir := opts.Dir
	if dir == "" {
		repo, err := RepoName(opts.URL)
		if err != nil {
			return Cloned{}, err
		}
		dir = repo
	}

	dir, err := filepath.Abs(dir)
	if err != nil {
		return Cloned{}, err
	}

	// Reject bad input before anything is cloned.
	entries, err := links.ParseEntries(opts.Links)
	if err != nil {
		return Cloned{}, err
	}
	if opts.Seed != "" && len(entries) == 0 {
		return Cloned{}, errors.New("nothing to seed: say which paths to copy with --link")
	}

	l := layout.FromContainer(dir)
	if _, err := os.Lstat(l.Main); err == nil {
		return Cloned{}, fmt.Errorf("%s already exists", l.Main)
	}

	_, statErr := os.Lstat(l.Container)
	createdContainer := errors.Is(statErr, os.ErrNotExist)

	if err := os.MkdirAll(l.Container, 0o755); err != nil {
		return Cloned{}, err
	}
	if _, err := gitx.Run(ctx, l.Container, "clone", opts.URL, layout.MainDir); err != nil {
		// Like git clone, leave nothing behind that this call created.
		if createdContainer {
			_ = os.RemoveAll(l.Container)
		}

		return Cloned{}, err
	}

	res := Cloned{Layout: l}

	// Before Init, which names the project only for JetBrains IDEs.
	if opts.IDE != "" {
		if err := config.SetIDE(l.Container, opts.IDE); err != nil {
			return res, fmt.Errorf("cloned to %s, but writing %s failed: %w", l.Main, layout.ConfigFile, err)
		}
	}

	init, err := Manager{Layout: l}.Init(ctx)
	res.Init = init
	if err != nil {
		return res, fmt.Errorf("cloned to %s, but init failed: %w", l.Main, err)
	}

	if _, err := links.AddToFile(l.Container, entries); err != nil {
		return res, fmt.Errorf("cloned to %s, but writing %s failed: %w", l.Main, layout.LinksFile, err)
	}
	if res.Init.Links, err = links.Load(l.Container); err != nil {
		return res, err
	}

	if opts.Seed != "" {
		if res.Seeded, err = links.Seed(opts.Seed, l.Main, entries); err != nil {
			return res, fmt.Errorf("cloned to %s, but seeding from %s failed: %w", l.Main, opts.Seed, err)
		}
	}

	return res, nil
}
