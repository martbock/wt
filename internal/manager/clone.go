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
	// written to wt.yml. Empty links nothing; it can be changed later with AddLinks and
	// RemoveLinks.
	Links []string

	// IDE is written to wt.yml as this repository's IDE: a preset such as "vscode" or a macOS
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

// Clone clones a repository into <dir>/main, initializes it and lists opts.Links in wt.yml.
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

	if res.Init, err = (Manager{Layout: l}).setUp(ctx, opts.IDE, entries); err != nil {
		return res, fmt.Errorf("cloned to %s, but %w", l.Main, err)
	}

	if opts.Seed != "" {
		if res.Seeded, err = links.Seed(opts.Seed, l.Main, entries); err != nil {
			return res, fmt.Errorf("cloned to %s, but seeding from %s failed: %w", l.Main, opts.Seed, err)
		}
	}

	return res, nil
}

// setUp initializes a main clone that has just been put in place, by clone or convert: it writes
// wt.yml with the IDE and the links, then runs Init.
func (m Manager) setUp(ctx context.Context, ideName string, entries []links.Entry) (InitResult, error) {
	// Before Init, which names the project only for JetBrains IDEs.
	configCreated, err := config.Ensure(m.Layout.Container)
	if err != nil {
		return InitResult{}, fmt.Errorf("writing %s failed: %w", layout.ConfigFile, err)
	}
	if ideName != "" {
		if err := config.SetIDE(m.Layout.Container, ideName); err != nil {
			return InitResult{ConfigCreated: configCreated}, fmt.Errorf("writing %s failed: %w", layout.ConfigFile, err)
		}
	}

	res, err := m.Init(ctx)
	res.ConfigCreated = configCreated
	if err != nil {
		return res, fmt.Errorf("init failed: %w", err)
	}

	if _, err := links.AddToFile(m.Layout.Container, entries); err != nil {
		return res, fmt.Errorf("writing %s failed: %w", layout.ConfigFile, err)
	}

	res.Links, err = links.Load(m.Layout.Container)

	return res, err
}
