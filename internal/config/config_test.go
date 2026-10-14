package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/config"
)

func file(dir string) string { return filepath.Join(dir, "wt.conf") }

func TestLoad_MissingFileSetsNothing(t *testing.T) {
	c, err := config.Load(t.TempDir())

	require.NoError(t, err)
	assert.Empty(t, c.IDE)
}

func TestLoad_IgnoresCommentsAndUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(file(dir), []byte("# my repo\nfuture = 1\n  ide=vscode  \n"), 0o644))

	c, err := config.Load(dir)

	require.NoError(t, err)
	assert.Equal(t, "vscode", c.IDE)
}

func TestSetIDE_CreatesReplacesAndRemoves(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, config.SetIDE(dir, "vscode"))
	raw, _ := os.ReadFile(file(dir))
	assert.Equal(t, "ide = vscode\n", string(raw))

	require.NoError(t, os.WriteFile(file(dir), []byte("# keep\nide = goland\nother = 1\nide = dup"), 0o644))
	require.NoError(t, config.SetIDE(dir, " cursor "))
	raw, _ = os.ReadFile(file(dir))
	assert.Equal(t, "# keep\nide = cursor\nother = 1\n", string(raw), "other lines stay, the repeat is dropped")

	require.NoError(t, config.SetIDE(dir, ""))
	raw, _ = os.ReadFile(file(dir))
	assert.Equal(t, "# keep\nother = 1\n", string(raw))

	require.NoError(t, os.WriteFile(file(dir), []byte("ide = x\n"), 0o644))
	require.NoError(t, config.SetIDE(dir, ""))
	assert.NoFileExists(t, file(dir), "a file with nothing left is removed")

	require.NoError(t, config.SetIDE(dir, ""), "removing what is not there is fine")
}

func TestSetIDE_AppendsToFileWithoutTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(file(dir), []byte("# note"), 0o644))

	require.NoError(t, config.SetIDE(dir, "vscode"))

	raw, _ := os.ReadFile(file(dir))
	assert.Equal(t, "# note\nide = vscode\n", string(raw))
}
