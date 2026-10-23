package manager_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/manager"
)

func infoOf(t *testing.T, m manager.Manager, slug string) manager.Info {
	t.Helper()

	info, err := m.Find(t.Context(), slug)
	require.NoError(t, err)

	return info
}

// originRoot is the directory with origin.git and the seed clone that setup builds.
func originRoot(m manager.Manager) string {
	return filepath.Dir(filepath.Dir(m.Layout.Container))
}

func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()

	write(t, filepath.Join(dir, name), content)
	git(t, dir, "add", name)
	git(t, dir, "commit", "-q", "-m", "add "+name)
}

func TestMergeState(t *testing.T) {
	m := setup(t)

	for _, slug := range []string{"fresh", "work", "rebased", "squashed", "merged-ff"} {
		_, err := m.Create(t.Context(), manager.CreateOptions{Slug: slug, SkipFetch: true})
		require.NoError(t, err)
	}
	seed := filepath.Join(originRoot(m), "seed")

	// merged-ff: main moved up to the branch.
	commitFile(t, m.Layout.WorktreePath("merged-ff"), "f.txt", "f")
	git(t, m.Layout.WorktreePath("merged-ff"), "push", "-q", "origin", "merged-ff:main")

	// work: a commit that is nowhere else.
	commitFile(t, m.Layout.WorktreePath("work"), "work.txt", "w")

	// rebased: the same change lands on main as a different commit, as a rebase merge does.
	commitFile(t, m.Layout.WorktreePath("rebased"), "r.txt", "r")
	git(t, seed, "pull", "-q", "origin", "main")
	commitFile(t, seed, "r.txt", "r")
	git(t, seed, "push", "-q", "origin", "main")

	// squashed: pushed, then its remote branch was deleted. The content never reached main.
	commitFile(t, m.Layout.WorktreePath("squashed"), "s.txt", "s")
	git(t, m.Layout.WorktreePath("squashed"), "push", "-q", "-u", "origin", "squashed")
	git(t, m.Layout.Main, "push", "-q", "origin", "--delete", "squashed")

	require.NoError(t, m.Fetch(t.Context()))

	tests := []struct {
		slug  string
		want  manager.MergeState
		label string
	}{
		{"fresh", manager.MergeEmpty, "no commits"},
		{"work", manager.MergeNone, "no"},
		{"rebased", manager.MergeMerged, "yes"},
		{"squashed", manager.MergeRemoteGone, "remote gone"},
		{"merged-ff", manager.MergeEmpty, "no commits"},
	}

	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			info := infoOf(t, m, tt.slug)

			assert.Equal(t, tt.want, info.Merge)
			assert.Equal(t, tt.label, info.Landed(nil).Label())
		})
	}
}

func TestMergeState_PartlyRebasedIsNotMerged(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "partial", SkipFetch: true})
	require.NoError(t, err)
	seed := filepath.Join(originRoot(m), "seed")

	commitFile(t, created.Path, "one.txt", "1")
	commitFile(t, created.Path, "two.txt", "2")
	git(t, seed, "pull", "-q", "origin", "main")
	commitFile(t, seed, "one.txt", "1")
	git(t, seed, "push", "-q", "origin", "main")
	require.NoError(t, m.Fetch(t.Context()))

	assert.Equal(t, manager.MergeNone, infoOf(t, m, "partial").Merge)
}

func TestInfoLanded_MergedPullRequestWinsUnlessThereAreNewCommits(t *testing.T) {
	squashed := manager.Info{Ahead: 3, Head: "abc", Merge: manager.MergeRemoteGone}

	assert.Equal(t, manager.MergeMerged, squashed.Landed(&manager.PullRequest{State: manager.PRMerged, HeadSHA: "abc"}))
	assert.Equal(t, manager.MergeMerged, squashed.Landed(&manager.PullRequest{State: manager.PRMerged}), "no sha to compare")
	assert.Equal(t, manager.MergeRemoteGone, squashed.Landed(&manager.PullRequest{State: manager.PRMerged, HeadSHA: "old"}),
		"commits made after the merge are not merged")
	assert.Equal(t, manager.MergeRemoteGone, squashed.Landed(&manager.PullRequest{State: manager.PRClosed, HeadSHA: "abc"}))
	assert.Equal(t, manager.MergeRemoteGone, squashed.Landed(nil))

	mergedByCommit := manager.Info{Ahead: 0, Head: "new", Merge: manager.MergeEmpty}
	assert.Equal(t, manager.MergeMerged, mergedByCommit.Landed(&manager.PullRequest{State: manager.PRMerged, HeadSHA: "old"}))
}

func TestMergeState_LabelsAreDistinct(t *testing.T) {
	seen := map[string]manager.MergeState{}
	for _, s := range []manager.MergeState{manager.MergeUnknown, manager.MergeNone, manager.MergeEmpty, manager.MergeMerged, manager.MergeRemoteGone} {
		label := s.Label()
		assert.NotContains(t, seen, label)
		seen[label] = s
	}
}

func TestRemove_StillKeepsUnmergedBranchAfterRefactor(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "gone-remote", SkipFetch: true})
	require.NoError(t, err)
	commitFile(t, created.Path, "x.txt", "x")
	git(t, created.Path, "push", "-q", "-u", "origin", "gone-remote")
	git(t, m.Layout.Main, "push", "-q", "origin", "--delete", "gone-remote")

	res, err := m.Remove(t.Context(), "gone-remote", manager.RemoveOptions{})
	require.NoError(t, err)

	assert.True(t, res.BranchDeleted, "a deleted remote branch still frees the local one")
	_, err = os.Stat(created.Path)
	assert.True(t, os.IsNotExist(err))
}

func TestPurgeCandidates(t *testing.T) {
	merged := func(sha string) manager.PullRequest {
		return manager.PullRequest{State: manager.PRMerged, HeadSHA: sha}
	}
	wt := func(slug, head string) manager.Info {
		return manager.Info{Slug: slug, Branch: slug, Head: head, Ahead: 1}
	}

	dirty := wt("dirty", "d")
	dirty.Dirty = true
	missing := wt("missing", "m")
	missing.Missing = true
	detached := wt("detached", "t")
	detached.Branch = ""
	fresh := manager.Info{Slug: "fresh", Branch: "fresh", Head: "origin-main"}

	infos := []manager.Info{wt("done", "a"), dirty, missing, detached, wt("moved", "new"), wt("open", "o"), wt("none", "n"), fresh}
	prs := manager.PRs{Looked: true, BySlug: map[string]manager.PullRequest{
		"done":     merged("a"),
		"dirty":    merged("d"),
		"missing":  merged("m"),
		"detached": merged("t"),
		"moved":    merged("old"),
		"open":     {State: manager.PROpen, HeadSHA: "o"},
		"fresh":    merged("an-old-pr-with-the-same-branch-name"),
	}}

	got := manager.PurgeCandidates(infos, prs)

	require.Len(t, got, 1)
	assert.Equal(t, "done", got[0].Slug)
}

func TestPurgeCandidates_NothingWithoutALookup(t *testing.T) {
	infos := []manager.Info{{Slug: "a", Branch: "a", Head: "x"}}
	by := map[string]manager.PullRequest{"a": {State: manager.PRMerged, HeadSHA: "x"}}

	assert.Empty(t, manager.PurgeCandidates(infos, manager.PRs{BySlug: by}), "not looked up")
	assert.Empty(t, manager.PurgeCandidates(infos, manager.PRs{Looked: true, Err: errors.New("offline"), BySlug: by}))
}

func TestPurge_RemovesWorktreesAndTheirBranches(t *testing.T) {
	m := setup(t)
	squashed, err := m.Create(t.Context(), manager.CreateOptions{Slug: "squashed", SkipFetch: true})
	require.NoError(t, err)
	commitFile(t, squashed.Path, "s.txt", "s")
	// Pushed, but the remote branch stays: a plain Remove keeps the local branch.
	git(t, squashed.Path, "push", "-q", "-u", "origin", "squashed")

	dirty, err := m.Create(t.Context(), manager.CreateOptions{Slug: "dirty", SkipFetch: true})
	require.NoError(t, err)
	write(t, filepath.Join(dirty.Path, "wip.txt"), "wip")

	res := m.Purge(t.Context(), []string{"squashed", "dirty"})

	require.Len(t, res, 2)
	require.NoError(t, res[0].Err)
	assert.True(t, res[0].Removed.BranchDeleted)
	assert.NoDirExists(t, squashed.Path)
	assert.Empty(t, git(t, m.Layout.Main, "branch", "--list", "squashed"))

	require.ErrorIs(t, res[1].Err, manager.ErrDirty, "a worktree that got dirty is left alone")
	assert.DirExists(t, dirty.Path)
}

func TestRemove_WorktreeWithSubmodule(t *testing.T) {
	m := setup(t)
	sub := filepath.Join(t.TempDir(), "sub.git")
	git(t, filepath.Dir(sub), "init", "-q", "-b", "main", sub)
	git(t, sub, "commit", "-q", "--allow-empty", "-m", "sub")

	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "with-sub", SkipFetch: true})
	require.NoError(t, err)
	git(t, created.Path, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vendor/sub")
	git(t, created.Path, "commit", "-q", "-m", "add submodule")
	git(t, created.Path, "push", "-q", "-u", "origin", "with-sub")
	git(t, m.Layout.Main, "push", "-q", "origin", "--delete", "with-sub")

	res, err := m.Remove(t.Context(), "with-sub", manager.RemoveOptions{})

	require.NoError(t, err)
	assert.True(t, res.BranchDeleted)
	assert.NoDirExists(t, created.Path)
}

func TestRemove_DirtySubmoduleIsStillRefused(t *testing.T) {
	m := setup(t)
	sub := filepath.Join(t.TempDir(), "sub.git")
	git(t, filepath.Dir(sub), "init", "-q", "-b", "main", sub)
	git(t, sub, "commit", "-q", "--allow-empty", "-m", "sub")

	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "dirty-sub", SkipFetch: true})
	require.NoError(t, err)
	git(t, created.Path, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vendor/sub")
	git(t, created.Path, "commit", "-q", "-m", "add submodule")
	write(t, filepath.Join(created.Path, "vendor", "sub", "wip.txt"), "wip")

	_, err = m.Remove(t.Context(), "dirty-sub", manager.RemoveOptions{})

	require.ErrorIs(t, err, manager.ErrDirty)
	assert.DirExists(t, created.Path)
}
