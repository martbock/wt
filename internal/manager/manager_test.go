package manager_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/manager"
)

func TestCreate_LinksSharedFilesAndNamesProject(t *testing.T) {
	m := setup(t)

	created, err := m.Create(t.Context(), manager.CreateOptions{Prefix: "PROJ-123", Slug: "fix-login-bug"})
	require.NoError(t, err)

	assert.Equal(t, "PROJ-123-fix-login-bug", created.Branch)
	assert.Equal(t, filepath.Join(m.Layout.Worktrees, "fix-login-bug"), created.Path)
	assert.Equal(t, "PROJ-123-fix-login-bug", git(t, created.Path, "branch", "--show-current"))

	// Local config is the very same file as in main/, reached through a relative symlink.
	link := filepath.Join(created.Path, "config", "local.yaml")
	target, err := os.Readlink(link)
	require.NoError(t, err)
	assert.False(t, filepath.IsAbs(target), "links should be relative, got %s", target)

	real, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(m.Layout.Main, "config", "local.yaml"), real)

	// The run configuration directory exists in main/ and is shared, so configs saved from a
	// worktree land in main/.
	runConfigs := filepath.Join(created.Path, ".idea", "runConfigurations")
	write(t, filepath.Join(runConfigs, "server.xml"), "<xml/>")
	assert.FileExists(t, filepath.Join(m.Layout.Main, ".idea", "runConfigurations", "server.xml"))

	name, err := os.ReadFile(filepath.Join(created.Path, ".idea", ".name"))
	require.NoError(t, err)
	assert.Equal(t, "repo › fix-login-bug", string(name))

	assert.Empty(t, git(t, created.Path, "status", "--porcelain"), "linked files must stay invisible to git")
}

func TestCreate_WithoutTicketNamesBranchAfterSlug(t *testing.T) {
	m := setup(t)

	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "spike"})
	require.NoError(t, err)

	assert.Equal(t, "spike", created.Branch)
}

func TestCreate_PrefixJoinsBranchOnly(t *testing.T) {
	m := setup(t)

	tests := []struct{ prefix, slug, branch string }{
		{"fix/", "slash", "fix/slash"},
		{"PROJ-1234-", "dash", "PROJ-1234-dash"},
		{"PROJ-1234", "bare-ticket", "PROJ-1234-bare-ticket"},
	}

	for _, tt := range tests {
		created, err := m.Create(t.Context(), manager.CreateOptions{Prefix: tt.prefix, Slug: tt.slug, SkipFetch: true})
		require.NoError(t, err, tt.prefix)

		assert.Equal(t, tt.branch, created.Branch)
		assert.Equal(t, filepath.Join(m.Layout.Worktrees, tt.slug), created.Path)
		assert.Equal(t, manager.SourceNew, created.Source)
		assert.Equal(t, tt.branch, git(t, created.Path, "branch", "--show-current"))
	}
}

func TestCreate_BranchesFromOriginNotLocalMain(t *testing.T) {
	m := setup(t)

	// A local commit on main must not leak into new worktrees.
	write(t, filepath.Join(m.Layout.Main, "local-only.txt"), "x")
	git(t, m.Layout.Main, "add", ".")
	git(t, m.Layout.Main, "commit", "-q", "--no-verify", "-m", "local")

	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "clean-base"})
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(created.Path, "local-only.txt"))
}

func TestCreate_Rejects(t *testing.T) {
	m := setup(t)
	_, err := m.Create(t.Context(), manager.CreateOptions{Slug: "taken"})
	require.NoError(t, err)

	tests := []struct {
		name string
		opts manager.CreateOptions
	}{
		{"uppercase slug", manager.CreateOptions{Slug: "Bad"}},
		{"slug with slash", manager.CreateOptions{Slug: "a/b"}},
		{"bad prefix", manager.CreateOptions{Prefix: "a b/", Slug: "ok"}},
		{"prefix with branch", manager.CreateOptions{Prefix: "fix/", Branch: "feat/x"}},
		{"neither slug nor branch", manager.CreateOptions{}},
		{"invalid branch name", manager.CreateOptions{Branch: "bad..name", Slug: "ok"}},
		{"option-like branch", manager.CreateOptions{Branch: "-D", Slug: "ok"}},
		{"branch checked out in main", manager.CreateOptions{Branch: "main", Slug: "ok"}},
		{"existing worktree", manager.CreateOptions{Slug: "taken"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.Create(t.Context(), tt.opts)
			assert.Error(t, err)
		})
	}
}

func TestCreate_BranchChecksOutExistingLocalBranch(t *testing.T) {
	m := setup(t)
	git(t, m.Layout.Main, "branch", "feat/login")

	created, err := m.Create(t.Context(), manager.CreateOptions{Branch: "feat/login", SkipFetch: true})
	require.NoError(t, err)

	assert.Equal(t, "feat-login", created.Slug, "slug is derived from the branch")
	assert.Equal(t, "feat/login", created.Branch)
	assert.Equal(t, manager.SourceLocal, created.Source)
	assert.Equal(t, "feat/login", git(t, created.Path, "branch", "--show-current"))
}

func TestCreate_BranchTracksRemoteOnlyBranch(t *testing.T) {
	m := setup(t)
	pushBranch(t, m, "feat/review-me", "pr.txt")

	// Without a fetch the branch is unknown, so it would be created from the base instead.
	created, err := m.Create(t.Context(), manager.CreateOptions{Branch: "feat/review-me", Slug: "pr-7"})
	require.NoError(t, err)

	assert.Equal(t, "pr-7", created.Slug)
	assert.Equal(t, manager.SourceRemote, created.Source)
	assert.FileExists(t, filepath.Join(created.Path, "pr.txt"))
	assert.Equal(t, "origin/feat/review-me", git(t, created.Path, "rev-parse", "--abbrev-ref", "@{upstream}"))
}

func TestCreate_BranchThatExistsNowhereIsCreatedFromBase(t *testing.T) {
	m := setup(t)

	created, err := m.Create(t.Context(), manager.CreateOptions{Branch: "feat/brand-new", SkipFetch: true})
	require.NoError(t, err)

	assert.Equal(t, manager.SourceNew, created.Source)
	assert.Equal(t, "origin/main", created.Base)
	assert.Equal(t, "feat/brand-new", git(t, created.Path, "branch", "--show-current"))
}

func TestList(t *testing.T) {
	m := setup(t)

	_, err := m.Create(t.Context(), manager.CreateOptions{Prefix: "PROJ-1", Slug: "beta"})
	require.NoError(t, err)
	alpha, err := m.Create(t.Context(), manager.CreateOptions{Slug: "alpha"})
	require.NoError(t, err)

	write(t, filepath.Join(alpha.Path, "new.txt"), "x")
	git(t, alpha.Path, "add", ".")
	git(t, alpha.Path, "commit", "-q", "-m", "work")
	write(t, filepath.Join(alpha.Path, "dirty.txt"), "x")

	infos, err := m.List(t.Context())
	require.NoError(t, err)

	require.Len(t, infos, 2)
	assert.Equal(t, "alpha", infos[0].Slug)
	assert.Equal(t, "alpha", infos[0].Branch)
	assert.Equal(t, 1, infos[0].Ahead)
	assert.True(t, infos[0].Dirty)
	assert.Equal(t, "beta", infos[1].Slug)
	assert.False(t, infos[1].Dirty)
}

func TestRemove_CleanWorktreeLeavesSharedFilesAlone(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "throwaway"})
	require.NoError(t, err)
	write(t, filepath.Join(created.Path, ".idea", "runConfigurations", "server.xml"), "<xml/>")

	res, err := m.Remove(t.Context(), "throwaway", manager.RemoveOptions{SkipFetch: true})
	require.NoError(t, err)

	assert.NoDirExists(t, created.Path)
	assert.True(t, res.BranchDeleted)
	assert.Empty(t, git(t, m.Layout.Main, "branch", "--list", "throwaway"))

	// Removing a worktree must never reach through the symlinks into main/.
	assert.FileExists(t, filepath.Join(m.Layout.Main, "config", "local.yaml"))
	assert.FileExists(t, filepath.Join(m.Layout.Main, ".idea", "runConfigurations", "server.xml"))
}

func TestRemove_KeepsBranchWithUnmergedCommits(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "keeper"})
	require.NoError(t, err)
	write(t, filepath.Join(created.Path, "work.txt"), "x")
	git(t, created.Path, "add", ".")
	git(t, created.Path, "commit", "-q", "-m", "work")

	res, err := m.Remove(t.Context(), "keeper", manager.RemoveOptions{SkipFetch: true})
	require.NoError(t, err)

	assert.NoDirExists(t, created.Path)
	assert.False(t, res.BranchDeleted)
	assert.NotEmpty(t, res.BranchKept)
	assert.NotEmpty(t, git(t, m.Layout.Main, "branch", "--list", "keeper"))
}

func TestRemove_DeletesBranchWhoseRemoteIsGone(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "shipped"})
	require.NoError(t, err)
	write(t, filepath.Join(created.Path, "work.txt"), "x")
	git(t, created.Path, "add", ".")
	git(t, created.Path, "commit", "-q", "-m", "work")
	git(t, created.Path, "push", "-q", "-u", "origin", "shipped")

	// What a squash-merged PR with an auto-deleted branch looks like from here.
	git(t, m.Layout.Main, "push", "-q", "origin", "--delete", "shipped")

	res, err := m.Remove(t.Context(), "shipped", manager.RemoveOptions{})
	require.NoError(t, err)

	assert.True(t, res.BranchDeleted)
}

func TestRemove_RefusesDirtyUnlessForced(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "messy"})
	require.NoError(t, err)
	write(t, filepath.Join(created.Path, "scratch.txt"), "x")

	_, err = m.Remove(t.Context(), "messy", manager.RemoveOptions{SkipFetch: true})
	require.ErrorIs(t, err, manager.ErrDirty)
	assert.DirExists(t, created.Path)

	_, err = m.Remove(t.Context(), "messy", manager.RemoveOptions{Force: true, SkipFetch: true})
	require.NoError(t, err)
	assert.NoDirExists(t, created.Path)
}

func TestRemove_UnknownSlug(t *testing.T) {
	m := setup(t)

	_, err := m.Remove(t.Context(), "nope", manager.RemoveOptions{})

	var notFound *manager.NotFoundError
	assert.ErrorAs(t, err, &notFound)
}

func TestInit_PreCommitHookProtectsMainOnly(t *testing.T) {
	m := setup(t)
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "task"})
	require.NoError(t, err)

	out, err := gitErr(m.Layout.Main, "commit", "--allow-empty", "-m", "nope")
	assert.Error(t, err, "commit in main/ should be refused, got: %s", out)
	assert.Contains(t, out, "refusing to commit in the main clone")

	out, err = gitErr(created.Path, "commit", "--allow-empty", "-m", "fine")
	assert.NoError(t, err, out)
}

func TestInit_IsIdempotentAndKeepsForeignHook(t *testing.T) {
	m := setup(t)

	res, err := m.Init(t.Context())
	require.NoError(t, err)
	assert.Equal(t, manager.HookCurrent, res.Hook)
	assert.False(t, res.ConfigCreated)

	hook := filepath.Join(m.Layout.Main, ".git", "hooks", "pre-commit")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	res, err = m.Init(t.Context())
	require.NoError(t, err)
	assert.Equal(t, manager.HookForeign, res.Hook)

	content, err := os.ReadFile(hook)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\nexit 0\n", string(content))
}

func TestRepoName(t *testing.T) {
	tests := map[string]string{
		"git@github.com:acme/api.git":             "api",
		"https://github.com/acme/api":             "api",
		"https://github.com/acme/api.git/":        "api",
		"ssh://git@gitlab.com/group/sub/repo.git": "repo",
		"/srv/git/api.git":                        "api",
		"git@example.com:api.git":                 "api",
	}

	for url, want := range tests {
		t.Run(url, func(t *testing.T) {
			got, err := manager.RepoName(url)

			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}

	_, err := manager.RepoName("nonsense")
	assert.Error(t, err)
}

// cloneFixture returns the directory holding an existing clone with local config, and routes
// the fake remote git@example.com:acme/fresh.git to its bare origin, so URL parsing is exercised.
func cloneFixture(t *testing.T) (existing manager.Manager, root string) {
	t.Helper()

	existing = setup(t)
	root = filepath.Dir(filepath.Dir(existing.Layout.Container))

	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+filepath.Join(root, "origin.git")+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "git@example.com:acme/fresh.git")

	return existing, root
}

const freshURL = "git@example.com:acme/fresh.git"

func TestClone_LinksNothingByDefault(t *testing.T) {
	existing, root := cloneFixture(t)

	got, err := manager.Clone(t.Context(), manager.CloneOptions{URL: freshURL, Dir: filepath.Join(root, "new")})
	require.NoError(t, err)

	assert.Empty(t, got.Init.Links)
	assert.Empty(t, got.Seeded)

	m := manager.Manager{Layout: got.Layout}
	infos, err := m.Links()
	require.NoError(t, err)
	assert.Empty(t, infos)

	// Even though the existing clone has local config, nothing leaks into the new worktree.
	require.FileExists(t, filepath.Join(existing.Layout.Main, "config", "local.yaml"))
	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "first"})
	require.NoError(t, err)
	assert.Empty(t, created.Links)
	assert.NoFileExists(t, filepath.Join(created.Path, "config", "local.yaml"))
}

func TestClone_RejectsBadInputBeforeCloning(t *testing.T) {
	existing, root := cloneFixture(t)
	dir := filepath.Join(root, "new")

	for name, opts := range map[string]manager.CloneOptions{
		"seed without links": {Seed: existing.Layout.Main},
		"absolute link":      {Links: []string{"/etc/passwd"}},
		"link out of main":   {Links: []string{"../x"}},
	} {
		t.Run(name, func(t *testing.T) {
			opts.URL, opts.Dir = freshURL, dir

			_, err := manager.Clone(t.Context(), opts)

			assert.Error(t, err)
			assert.NoDirExists(t, dir)
		})
	}
}

func TestClone_LaysOutSeedsAndInitializes(t *testing.T) {
	existing, root := cloneFixture(t)

	got, err := manager.Clone(t.Context(), manager.CloneOptions{
		URL:   freshURL,
		Dir:   filepath.Join(root, "new"),
		Links: []string{"config/local*.yaml", ".env"},
		Seed:  existing.Layout.Main,
	})
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(root, "new", "main"), got.Layout.Main)
	assert.Equal(t, "new", got.Layout.Repo)
	assert.Equal(t, manager.HookInstalled, got.Init.Hook)
	assert.FileExists(t, got.Init.ConfigFile)
	assert.Len(t, got.Init.Links, 2)

	local, err := os.ReadFile(filepath.Join(got.Layout.Main, "config", "local.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "secret: 1\n", string(local), "seeded from the existing clone")

	// The new clone is immediately usable.
	created, err := manager.Manager{Layout: got.Layout}.Create(t.Context(), manager.CreateOptions{Slug: "first"})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(created.Path, "config", "local.yaml"))

	_, err = manager.Clone(t.Context(), manager.CloneOptions{URL: freshURL, Dir: filepath.Join(root, "new")})
	assert.Error(t, err, "cloning over an existing main/ must fail")
}

func TestClone_DefaultsToTheRepositoryNameInTheCurrentDirectory(t *testing.T) {
	_, root := cloneFixture(t)
	cwd := filepath.Join(root, "cwd")
	require.NoError(t, os.MkdirAll(cwd, 0o755))
	t.Chdir(cwd)

	got, err := manager.Clone(t.Context(), manager.CloneOptions{URL: freshURL})
	require.NoError(t, err)

	cwd, err = filepath.EvalSymlinks(cwd)
	require.NoError(t, err)
	gotMain, err := filepath.EvalSymlinks(got.Layout.Main)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cwd, "fresh", "main"), gotMain)
}

func TestClone_LeavesNothingBehindWhenTheCloneFails(t *testing.T) {
	_, root := cloneFixture(t)
	dir := filepath.Join(root, "new")

	_, err := manager.Clone(t.Context(), manager.CloneOptions{URL: filepath.Join(root, "missing.git"), Dir: dir})

	assert.Error(t, err)
	assert.NoDirExists(t, dir)
}
