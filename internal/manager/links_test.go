package manager_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/links"
	"github.com/martbock/wt/internal/manager"
)

func isLink(t *testing.T, path string) bool {
	t.Helper()

	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false
	}
	require.NoError(t, err)

	return info.Mode()&os.ModeSymlink != 0
}

func actionsOf(c manager.LinkChange, slug string) map[string]links.Status {
	out := map[string]links.Status{}
	for _, w := range c.Worktrees {
		if w.Slug != slug {
			continue
		}
		for _, a := range w.Actions {
			out[a.Path] = a.Status
		}
	}

	return out
}

func TestInit_SharesNothingByDefault(t *testing.T) {
	m := setupUnlinked(t)

	infos, err := m.Links()
	require.NoError(t, err)
	assert.Empty(t, infos)

	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "plain"})
	require.NoError(t, err)

	assert.Empty(t, created.Links)
	assert.NoFileExists(t, filepath.Join(created.Path, "config", "local.yaml"))
	assert.NoDirExists(t, filepath.Join(created.Path, ".idea", "runConfigurations"))
}

func TestAddLinks_LinksIntoExistingWorktrees(t *testing.T) {
	m := setupUnlinked(t)
	first, err := m.Create(t.Context(), manager.CreateOptions{Slug: "first"})
	require.NoError(t, err)
	second, err := m.Create(t.Context(), manager.CreateOptions{Slug: "second"})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(first.Path, "config", "local.yaml"))

	change, err := m.AddLinks(t.Context(), []string{"config/local*.yaml", "docs/not-there.md"})
	require.NoError(t, err)

	assert.Len(t, change.Changed, 2)
	assert.Equal(t, map[string]links.Status{
		"config/local.yaml": links.Linked,
		"docs/not-there.md": links.Missing,
	}, actionsOf(change, "first"))
	assert.Len(t, change.Worktrees, 2)

	for _, wt := range []string{first.Path, second.Path} {
		link := filepath.Join(wt, "config", "local.yaml")
		assert.True(t, isLink(t, link), link)

		real, err := filepath.EvalSymlinks(link)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(m.Layout.Main, "config", "local.yaml"), real)

		assert.Empty(t, git(t, wt, "status", "--porcelain", "--", "config"), "new links stay invisible to git")
	}

	// Worktrees created afterwards get the link as well.
	third, err := m.Create(t.Context(), manager.CreateOptions{Slug: "third"})
	require.NoError(t, err)
	assert.True(t, isLink(t, filepath.Join(third.Path, "config", "local.yaml")))

	raw, err := os.ReadFile(filepath.Join(m.Layout.Container, "wt.links"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "config/local*.yaml\n")
}

func TestAddLinks_DirectoryIsSharedBothWays(t *testing.T) {
	m := setupUnlinked(t)
	wt, err := m.Create(t.Context(), manager.CreateOptions{Slug: "ide"})
	require.NoError(t, err)

	_, err = m.AddLinks(t.Context(), []string{".idea/runConfigurations/"})
	require.NoError(t, err)

	write(t, filepath.Join(wt.Path, ".idea", "runConfigurations", "server.xml"), "<xml/>")
	assert.FileExists(t, filepath.Join(m.Layout.Main, ".idea", "runConfigurations", "server.xml"))
}

func TestAddLinks_ListedPathRepairsWorktreesWithoutRewritingTheFile(t *testing.T) {
	m := setup(t)
	wt, err := m.Create(t.Context(), manager.CreateOptions{Slug: "broken"})
	require.NoError(t, err)
	link := filepath.Join(wt.Path, "config", "local.yaml")
	require.NoError(t, os.Remove(link))

	change, err := m.AddLinks(t.Context(), []string{"config/local*.yaml"})
	require.NoError(t, err)

	assert.Empty(t, change.Changed)
	assert.Len(t, change.Unchanged, 1)
	assert.True(t, isLink(t, link))
}

func TestAddLinks_KeepsFilesAWorktreeAlreadyHas(t *testing.T) {
	m := setupUnlinked(t)
	wt, err := m.Create(t.Context(), manager.CreateOptions{Slug: "own"})
	require.NoError(t, err)
	write(t, filepath.Join(wt.Path, "config", "local.yaml"), "worktree copy")

	change, err := m.AddLinks(t.Context(), []string{"config/local.yaml"})
	require.NoError(t, err)

	assert.Equal(t, links.Skipped, actionsOf(change, "own")["config/local.yaml"])
	got, err := os.ReadFile(filepath.Join(wt.Path, "config", "local.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "worktree copy", string(got))
}

func TestAddLinks_RejectsBadPathBeforeChangingAnything(t *testing.T) {
	m := setupUnlinked(t)

	_, err := m.AddLinks(t.Context(), []string{".env", "../escape"})
	require.Error(t, err)

	infos, err := m.Links()
	require.NoError(t, err)
	assert.Empty(t, infos)
}

func TestRemoveLinks_UnlinksExistingWorktreesAndKeepsMain(t *testing.T) {
	m := setup(t)
	wt, err := m.Create(t.Context(), manager.CreateOptions{Slug: "task"})
	require.NoError(t, err)
	write(t, filepath.Join(wt.Path, ".idea", "runConfigurations", "server.xml"), "<xml/>")
	require.True(t, isLink(t, filepath.Join(wt.Path, "config", "local.yaml")))

	change, err := m.RemoveLinks(t.Context(), []string{"config/local*.yaml", ".idea/runConfigurations"})
	require.NoError(t, err)

	assert.Len(t, change.Changed, 2)
	assert.Equal(t, map[string]links.Status{
		"config/local.yaml":       links.Unlinked,
		".idea/runConfigurations": links.Unlinked,
	}, actionsOf(change, "task"))

	assert.NoFileExists(t, filepath.Join(wt.Path, "config", "local.yaml"))
	assert.NoDirExists(t, filepath.Join(wt.Path, ".idea", "runConfigurations"))
	assert.FileExists(t, filepath.Join(wt.Path, ".idea", ".name"), "unrelated files in the same directory stay")

	// Taking a path out of the list must never delete the shared original.
	assert.FileExists(t, filepath.Join(m.Layout.Main, "config", "local.yaml"))
	assert.FileExists(t, filepath.Join(m.Layout.Main, ".idea", "runConfigurations", "server.xml"))

	infos, err := m.Links()
	require.NoError(t, err)
	require.Len(t, infos, 2)
	assert.Equal(t, ".env", infos[0].Entry.String())
	assert.Equal(t, ".env.*", infos[1].Entry.String())

	later, err := m.Create(t.Context(), manager.CreateOptions{Slug: "later"})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(later.Path, "config", "local.yaml"))
}

func TestRemoveLinks_UnknownPathChangesNothing(t *testing.T) {
	m := setup(t)
	wt, err := m.Create(t.Context(), manager.CreateOptions{Slug: "task"})
	require.NoError(t, err)

	_, err = m.RemoveLinks(t.Context(), []string{".env", "typo.yaml"})

	var notListed *links.NotListedError
	require.ErrorAs(t, err, &notListed)

	infos, err := m.Links()
	require.NoError(t, err)
	assert.Len(t, infos, len(sharedPaths))
	assert.True(t, isLink(t, filepath.Join(wt.Path, "config", "local.yaml")))
}

func TestSyncLinks_PicksUpHandEditedFile(t *testing.T) {
	m := setupUnlinked(t)
	wt, err := m.Create(t.Context(), manager.CreateOptions{Slug: "task"})
	require.NoError(t, err)

	write(t, filepath.Join(m.Layout.Container, "wt.links"), "# edited by hand\nconfig/local.yaml\n")

	change, err := m.SyncLinks(t.Context())
	require.NoError(t, err)

	assert.Equal(t, links.Linked, actionsOf(change, "task")["config/local.yaml"])
	assert.True(t, isLink(t, filepath.Join(wt.Path, "config", "local.yaml")))
	assert.Empty(t, git(t, wt.Path, "status", "--porcelain", "--", "config"))
}

func TestLinks_ReportsWhatEachEntrySelectsInMain(t *testing.T) {
	m := setupUnlinked(t)
	_, err := m.AddLinks(t.Context(), []string{"config/local*.yaml", ".env"})
	require.NoError(t, err)

	infos, err := m.Links()
	require.NoError(t, err)

	require.Len(t, infos, 2)
	assert.Equal(t, []string{filepath.Join("config", "local.yaml")}, infos[0].Matches)
	assert.Empty(t, infos[1].Matches, "a typo shows up as nothing selected")
}
