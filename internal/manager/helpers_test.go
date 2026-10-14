package manager_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/manager"
)

// git runs git in dir and fails the test if it does.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()

	out, err := gitErr(dir, args...)
	require.NoError(t, err, "git %s\n%s", strings.Join(args, " "), out)

	return out
}

func gitErr(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	return strings.TrimSpace(string(out)), err
}

func write(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// sharedPaths is what the tests share between worktrees. wt itself shares nothing by default.
var sharedPaths = []string{"config/local*.yaml", ".env", ".env.*", ".idea/runConfigurations/"}

// setup builds <tmp>/acme/repo/main as a clone of a bare origin, with gitignored local config
// in main/ and wt initialized with sharedPaths linked.
func setup(t *testing.T) manager.Manager {
	t.Helper()

	m := setupUnlinked(t)
	_, err := m.AddLinks(t.Context(), sharedPaths)
	require.NoError(t, err)

	return m
}

// setupUnlinked is setup with the freshly initialized, empty wt.links.
func setupUnlinked(t *testing.T) manager.Manager {
	t.Helper()

	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv(ide.AppEnv, "")
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
	write(t, filepath.Join(seed, ".gitignore"), "config/local*.yaml\n.idea/\n.env\n")
	write(t, filepath.Join(seed, "README.md"), "hello\n")
	git(t, seed, "add", ".")
	git(t, seed, "commit", "-q", "-m", "initial")
	git(t, seed, "push", "-q", "origin", "main")

	container := filepath.Join(root, "acme", "repo")
	require.NoError(t, os.MkdirAll(container, 0o755))
	git(t, container, "clone", "-q", origin, layout.MainDir)

	l := layout.FromContainer(container)
	write(t, filepath.Join(l.Main, "config", "local.yaml"), "secret: 1\n")

	m := manager.Manager{Layout: l}
	_, err = m.Init(t.Context())
	require.NoError(t, err)

	return m
}

// pushBranch publishes a branch with one commit to origin without creating it in main/, the way a
// teammate's pull request branch shows up.
func pushBranch(t *testing.T, m manager.Manager, branch, file string) {
	t.Helper()

	url := git(t, m.Layout.Main, "remote", "get-url", "origin")
	other := filepath.Join(t.TempDir(), "teammate")
	git(t, filepath.Dir(other), "clone", "-q", url, other)
	git(t, other, "switch", "-q", "-c", branch)
	write(t, filepath.Join(other, file), "x")
	git(t, other, "add", ".")
	git(t, other, "commit", "-q", "-m", "teammate work")
	git(t, other, "push", "-q", "origin", branch)
}
