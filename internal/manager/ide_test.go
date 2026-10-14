package manager_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/config"
	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/manager"
)

func TestIDE_RepoSettingBeatsEnvironmentBeatsDefault(t *testing.T) {
	m := setupUnlinked(t)

	got, err := m.IDE()
	require.NoError(t, err)
	assert.Equal(t, "GoLand", got.IDE.App)
	assert.Equal(t, manager.IDEFromDefault, got.Source)

	t.Setenv(ide.AppEnv, "vscode")
	got, err = m.IDE()
	require.NoError(t, err)
	assert.Equal(t, "Visual Studio Code", got.IDE.App)
	assert.Equal(t, manager.IDEFromEnv, got.Source)

	got, err = m.SetIDE("webstorm")
	require.NoError(t, err)
	assert.Equal(t, "WebStorm", got.IDE.App)
	assert.Equal(t, manager.IDEFromConfig, got.Source)

	got, err = m.SetIDE("")
	require.NoError(t, err)
	assert.Equal(t, manager.IDEFromEnv, got.Source, "unsetting falls back")
}

func TestIDE_OnlyJetBrainsGetsProjectName(t *testing.T) {
	m := setupUnlinked(t)
	_, err := m.SetIDE("vscode")
	require.NoError(t, err)

	created, err := m.Create(t.Context(), manager.CreateOptions{Slug: "node-task"})
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(created.Path, ".idea", ".name"))

	_, err = m.SetIDE("goland")
	require.NoError(t, err)

	created, err = m.Create(t.Context(), manager.CreateOptions{Slug: "go-task"})
	require.NoError(t, err)

	name, err := os.ReadFile(filepath.Join(created.Path, ".idea", ".name"))
	require.NoError(t, err)
	assert.Equal(t, "repo › go-task", string(name))
}

func TestClone_WritesIDEBeforeInit(t *testing.T) {
	_, root := cloneFixture(t)

	got, err := manager.Clone(t.Context(), manager.CloneOptions{URL: freshURL, Dir: filepath.Join(root, "new"), IDE: "vscode"})
	require.NoError(t, err)

	assert.Equal(t, "Visual Studio Code", got.Init.IDE.IDE.App)
	assert.Equal(t, manager.IDEFromConfig, got.Init.IDE.Source)
	assert.NoFileExists(t, filepath.Join(got.Layout.Main, ".idea", ".name"), "no JetBrains clutter in a VS Code repository")

	conf, err := os.ReadFile(filepath.Join(got.Layout.Container, "wt.yml"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(conf), config.Template), "the explanatory comments come along")
	assert.True(t, strings.HasSuffix(string(conf), "\nide: vscode\n"), string(conf))
}

func TestIDE_GlobalSettingComesAfterRepoAndEnvironment(t *testing.T) {
	m := setupUnlinked(t)
	require.NoError(t, config.SetGlobal(config.KeyIDE, "cursor"))

	got, err := m.IDE()
	require.NoError(t, err)
	assert.Equal(t, "Cursor", got.IDE.App)
	assert.Equal(t, manager.IDEFromGlobal, got.Source)

	t.Setenv(ide.AppEnv, "vscode")
	got, err = m.IDE()
	require.NoError(t, err)
	assert.Equal(t, manager.IDEFromEnv, got.Source, "the environment beats the global setting")

	got, err = m.SetIDE("webstorm")
	require.NoError(t, err)
	assert.Equal(t, "WebStorm", got.IDE.App)
	assert.Equal(t, manager.IDEFromConfig, got.Source, "the repository beats both")
}
