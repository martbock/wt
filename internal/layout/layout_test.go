package layout_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/layout"
)

func TestBranch(t *testing.T) {
	assert.Equal(t, "PROJ-123-fix-login-bug", layout.Branch("PROJ-123-", "fix-login-bug"))
	assert.Equal(t, "fix/login-bug", layout.Branch("fix/", "login-bug"))
	assert.Equal(t, "spike", layout.Branch("", "spike"))
}

func TestNormalizePrefix(t *testing.T) {
	tests := map[string]string{
		"":            "",
		"fix/":        "fix/",
		"chore/":      "chore/",
		"PROJ-1234-":  "PROJ-1234-",
		"PROJ-1234":   "PROJ-1234-",
		"feat/PROJ-1": "feat/PROJ-1",
		"hotfix":      "hotfix",
	}

	for in, want := range tests {
		got, err := layout.NormalizePrefix(in)

		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	for _, bad := range []string{"/fix", "fix //", "a b", "fix/..", "-x", "fix:", "~"} {
		_, err := layout.NormalizePrefix(bad)
		assert.Error(t, err, bad)
	}
}

func TestSlugFromBranch(t *testing.T) {
	tests := map[string]string{
		"feat/PROJ-1234-Login":         "feat-proj-1234-login",
		"fix/login_bug":                "fix-login-bug",
		"release/1.2.x":                "release-1-2-x",
		"already-a-slug":               "already-a-slug",
		"//weird//":                    "weird",
		strings.Repeat("a", 70) + "-b": strings.Repeat("a", 60),
	}

	for in, want := range tests {
		got, err := layout.SlugFromBranch(in)

		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := layout.SlugFromBranch("///")
	assert.Error(t, err)
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"a", "fix-login-bug", "v2-api", "x1"} {
		assert.NoError(t, layout.ValidateSlug(ok), ok)
	}

	for _, bad := range []string{"", "Upper", "under_score", "a/b", "-lead", "trail-", "double--dash", "sp ace", string(make([]byte, 61))} {
		assert.Error(t, layout.ValidateSlug(bad), bad)
	}
}

func TestIsTicket(t *testing.T) {
	assert.True(t, layout.IsTicket("PROJ-123"))
	assert.True(t, layout.IsTicket("AB-1"))
	assert.False(t, layout.IsTicket("proj-123"))
	assert.False(t, layout.IsTicket("fix-login-bug"))
	assert.False(t, layout.IsTicket("PROJ-"))
}

func TestSlugOf(t *testing.T) {
	l := layout.FromContainer("/g/acme/repo")

	slug, ok := l.SlugOf("/g/acme/repo/worktrees/task")
	assert.True(t, ok)
	assert.Equal(t, "task", slug)

	for _, path := range []string{"/g/acme/repo/main", "/g/acme/repo/worktrees", "/g/acme/repo/worktrees/task/sub", "/elsewhere"} {
		_, ok := l.SlugOf(path)
		assert.False(t, ok, path)
	}
}

func TestDiscover(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	mainDir := filepath.Join(root, "acme", "repo", "main")
	require.NoError(t, os.MkdirAll(mainDir, 0o755))
	run(t, mainDir, "init", "-q", "-b", "main")
	run(t, mainDir, "-c", "user.name=t", "-c", "user.email=t@e.x", "commit", "-q", "--allow-empty", "-m", "init")

	worktree := filepath.Join(root, "acme", "repo", "worktrees", "task")
	run(t, mainDir, "worktree", "add", "-q", "-b", "task", worktree)

	want := layout.FromContainer(filepath.Join(root, "acme", "repo"))
	assert.Equal(t, "repo", want.Repo)

	for _, dir := range []string{mainDir, worktree} {
		got, err := layout.Discover(t.Context(), dir)

		require.NoError(t, err, dir)
		assert.Equal(t, want, got, dir)
	}
}

func TestDiscover_FromContainerAndWorktreesFolder(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	container := filepath.Join(root, "acme", "repo")
	mainDir := filepath.Join(container, "main")
	require.NoError(t, os.MkdirAll(mainDir, 0o755))
	run(t, mainDir, "init", "-q", "-b", "main")
	require.NoError(t, os.MkdirAll(filepath.Join(container, "worktrees"), 0o755))

	want := layout.FromContainer(container)

	for _, dir := range []string{container, filepath.Join(container, "worktrees")} {
		got, err := layout.Discover(t.Context(), dir)

		require.NoError(t, err, dir)
		assert.Equal(t, want, got, dir)
	}
}

func TestDiscover_NotManaged(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	run(t, dir, "init", "-q")

	_, err = layout.Discover(t.Context(), dir)
	assert.ErrorIs(t, err, layout.ErrNotManaged)

	// Not in a repository at all, and no container above it.
	plain, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	_, err = layout.Discover(t.Context(), plain)
	assert.ErrorIs(t, err, layout.ErrNotManaged)
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
