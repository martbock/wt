package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/config"
)

func file(dir string) string { return filepath.Join(dir, "wt.yml") }

func read(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(raw)
}

func TestLoad_MissingFileSetsNothing(t *testing.T) {
	c, err := config.Load(t.TempDir())

	require.NoError(t, err)
	assert.Empty(t, c.IDE)
	assert.Empty(t, c.Links)
}

func TestLoad_IgnoresCommentsAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(file(dir), []byte("# my repo\nfuture: {a: 1}\nide:   vscode  \nlinks: [.env]\n"), 0o644))

	c, err := config.Load(dir)

	require.NoError(t, err)
	assert.Equal(t, "vscode", c.IDE)
	assert.Equal(t, []string{".env"}, c.Links)
}

func TestLoad_BadYAMLNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(file(dir), []byte("ide: [\n"), 0o644))

	_, err := config.Load(dir)

	assert.ErrorContains(t, err, "wt.yml")
}

func TestEnsure_CreatesTheTemplateOnce(t *testing.T) {
	dir := t.TempDir()

	created, err := config.Ensure(dir)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, config.Template, read(t, file(dir)))

	require.NoError(t, os.WriteFile(file(dir), []byte("ide: vscode\n"), 0o644))
	created, err = config.Ensure(dir)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "ide: vscode\n", read(t, file(dir)))
}

func TestTemplate_SetsNothing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(file(dir), []byte(config.Template), 0o644))

	c, err := config.Load(dir)

	require.NoError(t, err)
	assert.Equal(t, config.Config{}, c)
}

func TestSetIDE_CreatesReplacesAndRemoves(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, config.SetIDE(dir, "vscode"))
	assert.Equal(t, config.Template+"\nide: vscode\n", read(t, file(dir)), "a new file starts from the template")

	require.NoError(t, os.WriteFile(file(dir), []byte("# keep\n\nide: goland # old\nother: 1\nlinks:\n  - .env\n"), 0o644))
	require.NoError(t, config.SetIDE(dir, " cursor "))
	assert.Equal(t, "# keep\n\nide: cursor # old\nother: 1\nlinks:\n  - .env\n", read(t, file(dir)), "comments and other keys stay")

	require.NoError(t, config.SetIDE(dir, ""))
	assert.Equal(t, "# keep\n\nother: 1\nlinks:\n  - .env\n", read(t, file(dir)))

	require.NoError(t, config.SetIDE(dir, ""), "removing what is not there is fine")
}

func TestSetIDE_RefusesToOverwriteABrokenFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(file(dir), []byte("ide: [\n"), 0o644))

	require.Error(t, config.SetIDE(dir, "vscode"))
	assert.Equal(t, "ide: [\n", read(t, file(dir)))

	require.NoError(t, os.WriteFile(file(dir), []byte("- a\n- b\n"), 0o644))
	require.Error(t, config.SetIDE(dir, "vscode"), "a list is not a mapping of settings")
}

func TestGlobalFile_FollowsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := config.GlobalFile()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "wt", "config.yml"), path)

	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", dir)
	path, err = config.GlobalFile()

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".config", "wt", "config.yml"), path)
}

func TestSetGlobal_CreatesTheDirectoryAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "wt", "config.yml")

	c, err := config.LoadGlobal()
	require.NoError(t, err)
	assert.Empty(t, c.IDE)

	require.NoError(t, config.SetGlobal(config.KeyIDE, " vscode "))
	assert.Equal(t, "ide: vscode\n", read(t, path))
	c, err = config.LoadGlobal()
	require.NoError(t, err)
	assert.Equal(t, "vscode", c.IDE)
	assert.Equal(t, "vscode", c.Get(config.KeyIDE))

	require.NoError(t, os.WriteFile(path, []byte("# mine\nfuture: [1, 2]\nide: goland\n"), 0o644))
	require.NoError(t, config.SetGlobal(config.KeyIDE, "cursor"))
	assert.Equal(t, "# mine\nfuture: [1, 2]\nide: cursor\n", read(t, path), "comments and unknown keys stay")

	require.NoError(t, config.SetGlobal(config.KeyIDE, ""))
	assert.Equal(t, "# mine\nfuture: [1, 2]\n", read(t, path))

	require.NoError(t, os.WriteFile(path, []byte("ide: x\n"), 0o644))
	require.NoError(t, config.SetGlobal(config.KeyIDE, ""))
	assert.NoFileExists(t, path, "an empty global config is removed")
}

func TestGlobal_InvalidYAMLIsAnErrorNotAnOverwrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "wt", "config.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("ide: [\n"), 0o644))

	_, err := config.LoadGlobal()
	assert.ErrorContains(t, err, "config.yml")

	require.Error(t, config.SetGlobal(config.KeyIDE, "vscode"))
	assert.Equal(t, "ide: [\n", read(t, path), "a file that cannot be read is left alone")
}
