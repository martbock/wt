package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/martbock/wt/internal/gitx"
	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/links"
)

// CreateOptions describes a new worktree.
type CreateOptions struct {
	// Slug names the worktree directory. Empty derives it from Branch.
	Slug string

	// Prefix goes in front of the slug to form the name of a new branch, e.g. "fix/" or "PROJ-1234-".
	// A bare ticket ID gets its dash. It cannot be combined with Branch.
	Prefix string

	// Branch names the branch to work on, instead of one derived from Slug. It is checked out if
	// it exists locally or on origin and created from Base otherwise.
	Branch string

	// Base overrides the ref to branch from. Empty means the remote's default branch.
	Base string

	// SkipFetch branches from the refs as they are, without contacting the remote.
	SkipFetch bool
}

// Source says where the branch of a new worktree came from.
type Source int

const (
	// SourceNew is a branch created from Base.
	SourceNew Source = iota

	// SourceLocal is a local branch that already existed.
	SourceLocal

	// SourceRemote is a local branch created to track the same branch on origin.
	SourceRemote
)

// Created describes a new worktree.
type Created struct {
	Slug   string
	Branch string
	Path   string

	// Base is the ref a new branch was cut from. Empty unless Source is SourceNew.
	Base string

	Source Source
	Links  []links.Action
}

// Create adds a worktree, links the shared files into it and names the JetBrains project. If a
// step after `git worktree add` fails, the worktree stays and the error says so.
func (m Manager) Create(ctx context.Context, opts CreateOptions) (Created, error) {
	c, err := m.plan(opts)
	if err != nil {
		return Created{}, err
	}

	if _, err := os.Lstat(c.Path); err == nil {
		return Created{}, fmt.Errorf("%s already exists", c.Path)
	}

	if opts.Branch != "" {
		if _, err := gitx.Run(ctx, m.Layout.Main, "check-ref-format", "--branch", c.Branch); err != nil {
			return Created{}, fmt.Errorf("invalid branch name %q", c.Branch)
		}
	}

	if !opts.SkipFetch {
		if err := m.Fetch(ctx); err != nil {
			return Created{}, fmt.Errorf("fetching origin failed (use --no-fetch to skip): %w", err)
		}
	}

	if err := os.MkdirAll(m.Layout.Worktrees, 0o755); err != nil {
		return Created{}, err
	}

	if err := m.addWorktree(ctx, &c, opts.Base, opts.Branch != ""); err != nil {
		return Created{}, err
	}

	if err := m.decorate(ctx, &c); err != nil {
		return c, fmt.Errorf("worktree created at %s, but setting it up failed: %w", c.Path, err)
	}

	return c, nil
}

// plan validates opts and works out the slug, branch and path, without touching git.
func (m Manager) plan(opts CreateOptions) (Created, error) {
	c := Created{Slug: opts.Slug, Branch: opts.Branch}

	switch {
	case opts.Branch != "" && opts.Prefix != "":
		return Created{}, errors.New("--prefix names a new branch and cannot be combined with --branch")
	case opts.Branch != "":
		if strings.HasPrefix(opts.Branch, "-") {
			return Created{}, fmt.Errorf("invalid branch name %q", opts.Branch)
		}

		if c.Slug == "" {
			var err error
			if c.Slug, err = layout.SlugFromBranch(opts.Branch); err != nil {
				return Created{}, err
			}
		}
	case opts.Slug == "":
		return Created{}, errors.New("expected a <slug>, or --branch to name an existing branch")
	default:
		prefix, err := layout.NormalizePrefix(opts.Prefix)
		if err != nil {
			return Created{}, err
		}

		c.Branch = layout.Branch(prefix, opts.Slug)
	}

	if err := layout.ValidateSlug(c.Slug); err != nil {
		return Created{}, err
	}

	c.Path = m.Layout.WorktreePath(c.Slug)

	return c, nil
}

// addWorktree runs `git worktree add` for c.Branch. With an explicit --branch it checks the
// branch out if it exists locally, tracks origin's if only that exists, and otherwise, like a
// plain new worktree, cuts a new branch from base.
func (m Manager) addWorktree(ctx context.Context, c *Created, base string, explicit bool) error {
	add := func(args ...string) error {
		_, err := gitx.Run(ctx, m.Layout.Main, append([]string{"worktree", "add"}, args...)...)
		return err
	}

	if explicit {
		switch {
		case gitx.OK(ctx, m.Layout.Main, "show-ref", "--verify", "--quiet", "refs/heads/"+c.Branch):
			c.Source = SourceLocal

			return add(c.Path, c.Branch)
		case gitx.OK(ctx, m.Layout.Main, "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+c.Branch):
			c.Source = SourceRemote

			return add("--track", "-b", c.Branch, c.Path, "origin/"+c.Branch)
		}
	}

	c.Base = base
	if c.Base == "" {
		c.Base = m.DefaultBase(ctx)
	}

	// --no-track: the base is origin/main, and pushing should set the upstream, not pulling.
	return add("--no-track", "-b", c.Branch, c.Path, c.Base)
}

func (m Manager) decorate(ctx context.Context, c *Created) error {
	entries, err := links.Load(m.Layout.Container)
	if err != nil {
		return err
	}

	c.Links, err = m.linkWorktree(ctx, c.Path, entries)
	if err != nil {
		return err
	}

	chosen, err := m.IDE()
	if err != nil {
		return err
	}
	if !chosen.IDE.JetBrains {
		return nil
	}

	if err := ide.WriteName(c.Path, m.Layout.Repo+" › "+c.Slug); err != nil {
		return err
	}

	return m.ensureIgnored(ctx, c.Path, ide.NameFile)
}
