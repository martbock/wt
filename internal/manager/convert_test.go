package manager_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/manager"
)

// plainClone makes <tmp>/acme/repo a clone of a bare origin with the files in tracked committed,
// the way a clone that predates wt looks. It returns the clone.
func plainClone(t *testing.T, tracked map[string]string) string {
	t.Helper()

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(ide.AppEnv, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	origin := filepath.Join(root, "origin.git")
	git(t, root, "init", "-q", "--bare", "-b", "main", origin)

	seed := filepath.Join(root, "seed")
	git(t, root, "clone", "-q", origin, seed)
	write(t, filepath.Join(seed, ".gitignore"), ".env\n")
	for name, content := range tracked {
		write(t, filepath.Join(seed, name), content)
	}
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-q", "-m", "initial")
	git(t, seed, "push", "-q", "origin", "main")

	clone := filepath.Join(root, "acme", "repo")
	require.NoError(t, os.MkdirAll(filepath.Dir(clone), 0o755))
	git(t, filepath.Dir(clone), "clone", "-q", origin, "repo")

	return clone
}

func TestConvert_MovesCloneIntoMainAndSetsItUp(t *testing.T) {
	clone := plainClone(t, map[string]string{"README.md": "hello\n"})
	write(t, filepath.Join(clone, ".env"), "SECRET=1\n")
	write(t, filepath.Join(clone, "README.md"), "edited\n")

	res, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone, Links: []string{".env"}, IDE: "vscode"})
	require.NoError(t, err)

	l := layout.FromContainer(clone)
	assert.Equal(t, l, res.Layout)
	assert.DirExists(t, filepath.Join(l.Main, ".git"))
	assert.NoDirExists(t, filepath.Join(clone, ".git"))
	assert.DirExists(t, l.Worktrees)
	assert.FileExists(t, filepath.Join(l.Container, layout.ConfigFile))

	// The clone comes across untouched: ignored files and uncommitted edits included.
	assert.FileExists(t, filepath.Join(l.Main, ".env"))
	edited, err := os.ReadFile(filepath.Join(l.Main, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "edited\n", string(edited))

	assert.Equal(t, "main", res.Branch)
	assert.Equal(t, "main", res.DefaultBranch)
	assert.True(t, res.Dirty)
	assert.Equal(t, manager.HookInstalled, res.Init.Hook)
	require.Len(t, res.Init.Links, 1)

	// It is a managed repository now: found from main/, and worktrees work and share .env.
	found, err := layout.Discover(t.Context(), l.Main)
	require.NoError(t, err)
	assert.Equal(t, l, found)

	created, err := manager.Manager{Layout: l}.Create(t.Context(), manager.CreateOptions{Slug: "spike"})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(created.Path, ".env"))

	_, err = os.Lstat(filepath.Join(l.Container, "wt.yml"))
	require.NoError(t, err)
	chosen, err := manager.Manager{Layout: l}.IDE()
	require.NoError(t, err)
	assert.Equal(t, manager.IDEFromConfig, chosen.Source)
}

func TestConvert_CleanClone(t *testing.T) {
	clone := plainClone(t, map[string]string{"README.md": "hello\n"})

	res, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone})
	require.NoError(t, err)

	assert.False(t, res.Dirty)
	assert.Empty(t, res.Init.Links)
}

func TestConvert_KeepsNamesInsideTheClone(t *testing.T) {
	// A repository may have a main/, worktrees/ or wt.yml of its own: nothing collides, because the
	// clone is moved as a whole.
	clone := plainClone(t, map[string]string{
		"main/main.go":        "package main\n",
		"worktrees/notes.txt": "x\n",
		"wt.yml":              "tracked: true\n",
	})

	_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone})
	require.NoError(t, err)

	l := layout.FromContainer(clone)
	assert.FileExists(t, filepath.Join(l.Main, "main", "main.go"))
	assert.FileExists(t, filepath.Join(l.Main, "worktrees", "notes.txt"))
	assert.FileExists(t, filepath.Join(l.Main, "wt.yml"))
	assert.Empty(t, git(t, l.Main, "status", "--porcelain"))
}

func TestConvert_ReportsOtherBranch(t *testing.T) {
	clone := plainClone(t, map[string]string{"README.md": "hello\n"})
	git(t, clone, "switch", "-q", "-c", "feature")

	res, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone})
	require.NoError(t, err)

	assert.Equal(t, "feature", res.Branch)
	assert.Equal(t, "main", res.DefaultBranch)
}

func TestConvert_RefusesWhatItCannotConvert(t *testing.T) {
	t.Run("not a repository", func(t *testing.T) {
		clone := plainClone(t, map[string]string{"README.md": "x"})
		dir := filepath.Join(filepath.Dir(clone), "plain")
		require.NoError(t, os.Mkdir(dir, 0o755))

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: dir})
		require.ErrorContains(t, err, "no .git directory")
		assert.DirExists(t, dir)
	})

	t.Run("subdirectory of a clone", func(t *testing.T) {
		clone := plainClone(t, map[string]string{"sub/file.txt": "x"})

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: filepath.Join(clone, "sub")})
		require.ErrorContains(t, err, "no .git directory")
	})

	t.Run("linked worktree", func(t *testing.T) {
		clone := plainClone(t, map[string]string{"README.md": "x"})
		linked := filepath.Join(filepath.Dir(clone), "linked")
		git(t, clone, "worktree", "add", "-q", "-b", "other", linked)

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: linked})
		require.ErrorContains(t, err, "linked worktree")
	})

	t.Run("clone with linked worktrees", func(t *testing.T) {
		clone := plainClone(t, map[string]string{"README.md": "x"})
		linked := filepath.Join(filepath.Dir(clone), "linked")
		git(t, clone, "worktree", "add", "-q", "-b", "other", linked)

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone})
		require.ErrorContains(t, err, "1 linked worktrees")
		assert.DirExists(t, filepath.Join(clone, ".git"), "nothing should have moved")
	})

	t.Run("already a main clone", func(t *testing.T) {
		m := setup(t)

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: m.Layout.Main})
		require.ErrorContains(t, err, "wt init")
	})

	t.Run("bad link", func(t *testing.T) {
		clone := plainClone(t, map[string]string{"README.md": "x"})

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone, Links: []string{"../outside"}})
		require.Error(t, err)
		assert.DirExists(t, filepath.Join(clone, ".git"), "nothing should have moved")
	})

	t.Run("symlink", func(t *testing.T) {
		clone := plainClone(t, map[string]string{"README.md": "x"})
		link := filepath.Join(filepath.Dir(clone), "link")
		require.NoError(t, os.Symlink(clone, link))

		_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: link})
		require.ErrorContains(t, err, "not a directory")
	})
}

func TestConvert_LeavesNoParkingDirectory(t *testing.T) {
	clone := plainClone(t, map[string]string{"README.md": "x"})

	_, err := manager.Convert(t.Context(), manager.ConvertOptions{Dir: clone})
	require.NoError(t, err)

	entries, err := os.ReadDir(filepath.Dir(clone))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the container should be left next to its siblings")
}
