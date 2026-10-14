package manager

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/martbock/wt/internal/gitx"
)

// Info describes one linked worktree.
type Info struct {
	Slug   string
	Branch string
	Path   string

	// Dirty is true when there are uncommitted or untracked changes.
	Dirty bool

	// Head is the commit the worktree is on.
	Head string

	// Ahead counts commits on the branch that the base does not have.
	Ahead int

	// Merge says whether the branch's work is in the base, as far as git alone can tell. A
	// squash merge shows up as MergeRemoteGone at best; a PR lookup settles it (see Landed).
	Merge MergeState

	// Missing is true when the directory is gone but git still lists the worktree.
	Missing bool
}

// List returns the linked worktrees under <container>/worktrees, sorted by slug.
func (m Manager) List(ctx context.Context) ([]Info, error) {
	out, err := gitx.Run(ctx, m.Layout.Main, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	base := m.DefaultBase(ctx)
	gone := m.goneBranches(ctx)

	var infos []Info
	for _, block := range strings.Split(out, "\n\n") {
		info, ok := m.parseBlock(block)
		if !ok {
			continue
		}

		if info.Missing {
			infos = append(infos, info)
			continue
		}

		if status, err := gitx.Run(ctx, info.Path, "status", "--porcelain"); err == nil {
			info.Dirty = status != ""
		}
		if info.Branch != "" {
			info.Ahead = m.ahead(ctx, base, info.Branch)
			info.Merge = m.mergeState(ctx, base, info, gone[info.Branch])
		}

		infos = append(infos, info)
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].Slug < infos[j].Slug })

	return infos, nil
}

func (m Manager) parseBlock(block string) (Info, bool) {
	var info Info
	var prunable bool

	for _, line := range strings.Split(block, "\n") {
		key, value, _ := strings.Cut(line, " ")

		switch key {
		case "worktree":
			info.Path = value
		case "HEAD":
			info.Head = value
		case "branch":
			info.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "prunable":
			prunable = true
		}
	}

	slug, ok := m.Layout.SlugOf(info.Path)
	if !ok {
		return Info{}, false
	}

	info.Slug = slug
	info.Missing = prunable

	return info, true
}

func (m Manager) ahead(ctx context.Context, base, branch string) int {
	out, err := gitx.Run(ctx, m.Layout.Main, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0
	}

	n, _ := strconv.Atoi(out)

	return n
}

// Find returns the worktree with the given slug.
func (m Manager) Find(ctx context.Context, slug string) (Info, error) {
	infos, err := m.List(ctx)
	if err != nil {
		return Info{}, err
	}

	for _, info := range infos {
		if info.Slug == slug {
			return info, nil
		}
	}

	return Info{}, &NotFoundError{Slug: slug}
}

// NotFoundError means no worktree has the requested slug.
type NotFoundError struct{ Slug string }

func (e *NotFoundError) Error() string {
	return "no worktree named " + strconv.Quote(e.Slug) + " (see 'wt ls')"
}
