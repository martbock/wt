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

// fakeGH puts a gh on PATH that answers 'pr list --head <branch>' from a table.
func fakeGH(t *testing.T, script string) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const ghTable = `
[ "$1 $2" = "pr list" ] || { echo "unexpected: $*" >&2; exit 2; }
while [ $# -gt 0 ]; do [ "$1" = "--head" ] && branch="$2"; shift; done
case "$branch" in
  opened)  echo '[{"number":21,"state":"OPEN","isDraft":false,"url":"https://x/21","headRefOid":"aaa","reviewDecision":"APPROVED"}]';;
  drafted) echo '[{"number":22,"state":"OPEN","isDraft":true,"url":"https://x/22","headRefOid":"bbb","reviewDecision":""}]';;
  landed)  echo '[{"number":23,"state":"MERGED","isDraft":false,"url":"https://x/23","headRefOid":"ccc","reviewDecision":"APPROVED"}]';;
  reused)  echo '[{"number":30,"state":"CLOSED","headRefOid":"d1"},{"number":31,"state":"MERGED","headRefOid":"d2"},{"number":29,"state":"OPEN","headRefOid":"d3"},{"number":32,"state":"CLOSED","headRefOid":"d4"}]';;
  *)       echo '[]';;
esac
`

func TestPullRequests(t *testing.T) {
	fakeGH(t, ghTable)
	m := setup(t)
	for _, slug := range []string{"opened", "drafted", "landed", "reused", "nopr"} {
		_, err := m.Create(t.Context(), manager.CreateOptions{Slug: slug, SkipFetch: true})
		require.NoError(t, err)
	}
	infos, err := m.List(t.Context())
	require.NoError(t, err)

	prs := m.PullRequests(t.Context(), infos)

	require.NoError(t, prs.Err)
	assert.True(t, prs.Looked)
	assert.Equal(t, "#21 approved", prs.Label("opened"))
	assert.Equal(t, "#22 draft", prs.Label("drafted"))
	assert.Equal(t, "#23 merged", prs.Label("landed"))
	assert.Equal(t, "#29 open", prs.Label("reused"), "an open pull request beats newer closed ones")
	assert.Equal(t, "none", prs.Label("nopr"))
	assert.Equal(t, "https://x/21", prs.For("opened").URL)
	assert.Nil(t, prs.For("nopr"))
}

func TestPullRequests_LatestWhenNoneIsOpen(t *testing.T) {
	fakeGH(t, `echo '[{"number":3,"state":"CLOSED"},{"number":8,"state":"MERGED"},{"number":5,"state":"CLOSED"}]'`)
	m := setup(t)
	_, err := m.Create(t.Context(), manager.CreateOptions{Slug: "again", SkipFetch: true})
	require.NoError(t, err)
	infos, err := m.List(t.Context())
	require.NoError(t, err)

	prs := m.PullRequests(t.Context(), infos)

	assert.Equal(t, "#8 merged", prs.Label("again"))
}

func TestPullRequests_MergedPullRequestSettlesASquashMerge(t *testing.T) {
	fakeGH(t, ghTable)
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "landed", SkipFetch: true})
	require.NoError(t, err)
	commitFile(t, created.Path, "l.txt", "l")
	info := infoOf(t, m, "landed")
	require.Equal(t, manager.MergeNone, info.Merge, "git alone cannot see a squash merge")

	// The table's merged pull request was last seen at "ccc"; the worktree is on something else.
	prs := m.PullRequests(t.Context(), []manager.Info{info})
	assert.Equal(t, manager.MergeNone, info.Landed(prs.For("landed")), "commits after the merge")

	info.Head = "ccc"
	assert.Equal(t, manager.MergeMerged, info.Landed(prs.For("landed")))
}

func TestPullRequests_OneFailureMakesThemAllUnknown(t *testing.T) {
	fakeGH(t, `
while [ $# -gt 0 ]; do [ "$1" = "--head" ] && branch="$2"; shift; done
if [ "$branch" = "bad" ]; then echo "HTTP 401: Bad credentials" >&2; echo "second line" >&2; exit 1; fi
echo '[{"number":1,"state":"OPEN"}]'
`)
	m := setup(t)
	for _, slug := range []string{"good", "bad"} {
		_, err := m.Create(t.Context(), manager.CreateOptions{Slug: slug, SkipFetch: true})
		require.NoError(t, err)
	}
	infos, err := m.List(t.Context())
	require.NoError(t, err)

	prs := m.PullRequests(t.Context(), infos)

	assert.EqualError(t, prs.Err, "bad: HTTP 401: Bad credentials")
	assert.Equal(t, "?", prs.Label("good"), "a partial answer would read as 'no pull request'")
	assert.Equal(t, "?", prs.Label("bad"))
	assert.Nil(t, prs.For("good"))
}

func TestPullRequests_WithoutGH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := manager.Manager{}

	prs := m.PullRequests(t.Context(), []manager.Info{{Slug: "x", Branch: "x"}})

	assert.True(t, errors.Is(prs.Err, manager.ErrNoGH))
	assert.Equal(t, "?", prs.Label("x"))
}

func TestPullRequests_BadOutput(t *testing.T) {
	fakeGH(t, `echo 'not json'`)
	m := setup(t)
	_, err := m.Create(t.Context(), manager.CreateOptions{Slug: "x", SkipFetch: true})
	require.NoError(t, err)
	infos, err := m.List(t.Context())
	require.NoError(t, err)

	prs := m.PullRequests(t.Context(), infos)

	require.Error(t, prs.Err)
	assert.Contains(t, prs.Err.Error(), "unexpected output from gh")
}

func TestPRs_LabelBeforeLookup(t *testing.T) {
	assert.Equal(t, "…", manager.PRs{}.Label("x"))
}

func TestPullRequestLabel(t *testing.T) {
	tests := []struct {
		pr   manager.PullRequest
		want string
	}{
		{manager.PullRequest{Number: 1, State: manager.PROpen}, "#1 open"},
		{manager.PullRequest{Number: 2, State: manager.PROpen, Draft: true}, "#2 draft"},
		{manager.PullRequest{Number: 3, State: manager.PROpen, Review: "CHANGES_REQUESTED"}, "#3 changes requested"},
		{manager.PullRequest{Number: 4, State: manager.PROpen, Review: "REVIEW_REQUIRED"}, "#4 open"},
		{manager.PullRequest{Number: 5, State: manager.PRMerged, Review: "APPROVED"}, "#5 merged"},
		{manager.PullRequest{Number: 6, State: manager.PRClosed}, "#6 closed"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.pr.Label())
	}
}
