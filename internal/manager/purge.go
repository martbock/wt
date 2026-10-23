package manager

import (
	"context"
)

// PurgeCandidates picks the worktrees that are done and safe to delete: clean, with no
// uncommitted or untracked changes, and sitting exactly on the tip of a merged pull request, so
// no commit is lost. A branch that has moved on since the merge, or has no pull request, is not
// a candidate. Without a successful lookup nothing is.
func PurgeCandidates(infos []Info, prs PRs) []Info {
	if !prs.Looked || prs.Err != nil {
		return nil
	}

	var out []Info
	for _, info := range infos {
		pr := prs.For(info.Slug)

		switch {
		case info.Missing, info.Dirty, info.Branch == "", pr == nil:
		case pr.State != PRMerged, pr.HeadSHA == "", pr.HeadSHA != info.Head:
		default:
			out = append(out, info)
		}
	}

	return out
}

// Purged is what happened to one worktree in Purge.
type Purged struct {
	Slug    string
	Removed Removed
	Err     error
}

// Purge removes the given worktrees, which PurgeCandidates picked, and their branches: the
// pull request is merged, so the branch holds nothing that is not in the base. A worktree that
// has become dirty since is left alone and reported with its error. Origin is fetched once.
func (m Manager) Purge(ctx context.Context, slugs []string) []Purged {
	// Best effort, like in Remove.
	_ = m.Fetch(ctx)

	out := make([]Purged, 0, len(slugs))
	for _, slug := range slugs {
		res, err := m.Remove(ctx, slug, RemoveOptions{SkipFetch: true, BranchMerged: true})
		out = append(out, Purged{Slug: slug, Removed: res, Err: err})
	}

	return out
}
