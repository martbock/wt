package manager_test

import (
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
