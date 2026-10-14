package manager

import (
	"context"
	"fmt"

	"github.com/martbock/wt/internal/links"
)

// WorktreeLinks is what a change to the links in wt.yml did in one worktree.
type WorktreeLinks struct {
	Slug    string
	Actions []links.Action
}

// LinkChange reports a change to the links in wt.yml and its effect on the existing worktrees.
type LinkChange struct {
	// Changed are the entries added to or removed from the links in wt.yml.
	Changed []links.Entry

	// Unchanged are the entries that were already listed (adding), so only re-applied.
	Unchanged []links.Entry

	Worktrees []WorktreeLinks
}

// LinkInfo is one entry of the links in wt.yml and what it selects in main/.
type LinkInfo struct {
	Entry links.Entry

	// Matches are the paths in main/ that the entry selects. Empty when there are none yet.
	Matches []string
}

// Links lists the entries of the links in wt.yml.
func (m Manager) Links() ([]LinkInfo, error) {
	entries, err := links.Load(m.Layout.Container)
	if err != nil {
		return nil, err
	}

	infos := make([]LinkInfo, 0, len(entries))
	for _, e := range entries {
		matches, err := links.Match(m.Layout.Main, e)
		if err != nil {
			return nil, err
		}
		infos = append(infos, LinkInfo{Entry: e, Matches: matches})
	}

	return infos, nil
}

// AddLinks lists paths in the links of wt.yml and symlinks them into every existing worktree. Paths that
// are already listed are applied again, which repairs worktrees that lack the link.
func (m Manager) AddLinks(ctx context.Context, paths []string) (LinkChange, error) {
	entries, err := links.ParseEntries(paths)
	if err != nil {
		return LinkChange{}, err
	}

	var change LinkChange
	if change.Changed, err = links.AddToFile(m.Layout.Container, entries); err != nil {
		return change, err
	}
	for _, e := range entries {
		if !containsEntry(change.Changed, e) {
			change.Unchanged = append(change.Unchanged, e)
		}
	}

	change.Worktrees, err = m.eachWorktree(ctx, func(path string) ([]links.Action, error) {
		return m.linkWorktree(ctx, path, entries)
	})

	return change, err
}

// RemoveLinks drops paths from the links of wt.yml and removes their symlinks from every existing worktree.
// Files in main/ are never touched, and a path that is not listed is an error that changes
// nothing.
func (m Manager) RemoveLinks(ctx context.Context, paths []string) (LinkChange, error) {
	entries, err := links.ParseEntries(paths)
	if err != nil {
		return LinkChange{}, err
	}

	var change LinkChange
	if change.Changed, err = links.RemoveFromFile(m.Layout.Container, entries); err != nil {
		return change, err
	}

	change.Worktrees, err = m.eachWorktree(ctx, func(path string) ([]links.Action, error) {
		return links.Unlink(m.Layout.Main, path, change.Changed)
	})

	return change, err
}

// SyncLinks applies the links in wt.yml as it is now to every existing worktree, for after editing the
// file by hand. It only adds links: use RemoveLinks to take paths away.
func (m Manager) SyncLinks(ctx context.Context) (LinkChange, error) {
	entries, err := links.Load(m.Layout.Container)
	if err != nil {
		return LinkChange{}, err
	}

	change := LinkChange{Unchanged: entries}
	change.Worktrees, err = m.eachWorktree(ctx, func(path string) ([]links.Action, error) {
		return m.linkWorktree(ctx, path, entries)
	})

	return change, err
}

// eachWorktree runs fn on every worktree whose directory exists. It stops at the first error and
// returns what was done up to then.
func (m Manager) eachWorktree(ctx context.Context, fn func(path string) ([]links.Action, error)) ([]WorktreeLinks, error) {
	infos, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	var done []WorktreeLinks
	for _, info := range infos {
		if info.Missing {
			continue
		}

		actions, err := fn(info.Path)
		done = append(done, WorktreeLinks{Slug: info.Slug, Actions: actions})
		if err != nil {
			return done, fmt.Errorf("%s: %w", info.Slug, err)
		}
	}

	return done, nil
}

// linkWorktree symlinks entries into the worktree at path and keeps the new links out of git.
func (m Manager) linkWorktree(ctx context.Context, path string, entries []links.Entry) ([]links.Action, error) {
	actions, err := links.Apply(m.Layout.Main, path, entries)
	if err != nil {
		return actions, err
	}

	var linked []string
	for _, a := range actions {
		if a.Status == links.Linked {
			linked = append(linked, a.Path)
		}
	}

	return actions, m.ensureIgnored(ctx, path, linked...)
}

func containsEntry(entries []links.Entry, want links.Entry) bool {
	for _, e := range entries {
		if e.Pattern == want.Pattern {
			return true
		}
	}

	return false
}
