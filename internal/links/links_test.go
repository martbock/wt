package links_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/config"
	"github.com/martbock/wt/internal/links"
)

func write(t *testing.T, path, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func statuses(actions []links.Action) map[string]links.Status {
	out := make(map[string]links.Status, len(actions))
	for _, a := range actions {
		out[a.Path] = a.Status
	}

	return out
}

func TestLoad(t *testing.T) {
	dir := container(t, "# comment\nlinks:\n  - config/local*.yaml\n  - '  .env  '\n  - .idea/runConfigurations/\n")

	got, err := links.Load(dir)

	require.NoError(t, err)
	assert.Equal(t, []links.Entry{
		{Pattern: "config/local*.yaml"},
		{Pattern: ".env"},
		{Pattern: ".idea/runConfigurations", Dir: true},
	}, got)
}

func TestLoad_NothingListedMeansNothingLinked(t *testing.T) {
	for name, content := range map[string]string{
		"no file":     "",
		"no list":     "ide: vscode\n",
		"empty list":  "links: []\n",
		"null list":   "links:\n",
		"the default": config.Template,
	} {
		got, err := links.Load(container(t, content))

		require.NoError(t, err, name)
		assert.Empty(t, got, name)
	}
}

func TestLoad_RejectsBadFiles(t *testing.T) {
	for name, content := range map[string]string{
		"not a list":      "links: .env\n",
		"not yaml":        "links: [\n",
		"outside main":    "links:\n  - ../secrets\n",
		"absolute":        "links:\n  - /etc/passwd\n",
		"all of main":     "links:\n  - .\n",
		"bad glob":        "links:\n  - config/[\n",
		"empty item":      "links:\n  - ''\n",
		"nested sequence": "links:\n  - [a]\n",
	} {
		_, err := links.Load(container(t, content))
		assert.Error(t, err, name)
	}
}

func TestParseEntry_RejectsPathsOutsideMain(t *testing.T) {
	for _, line := range []string{"/etc/passwd", "../secrets", ".."} {
		_, err := links.ParseEntry(line)
		assert.Error(t, err, line)
	}
}

func TestParseEntry_RejectsPathsThatWouldLinkAllOfMain(t *testing.T) {
	for _, line := range []string{".", "./", "a/.."} {
		_, err := links.ParseEntry(line)
		assert.Error(t, err, line)
	}
}

func TestParseEntry_RejectsBadGlob(t *testing.T) {
	_, err := links.ParseEntry("config/[")
	assert.Error(t, err)
}

func TestEntry_String(t *testing.T) {
	e, err := links.ParseEntry("./.idea/runConfigurations/")

	require.NoError(t, err)
	assert.Equal(t, ".idea/runConfigurations/", e.String())
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	mainDir, wt := filepath.Join(dir, "main"), filepath.Join(dir, "wt")
	write(t, filepath.Join(mainDir, "config", "local.yaml"), "a")
	write(t, filepath.Join(mainDir, "config", "local.staging.yaml"), "b")
	write(t, filepath.Join(mainDir, ".env.example"), "tracked")
	write(t, filepath.Join(wt, ".env.example"), "tracked")
	require.NoError(t, os.MkdirAll(wt, 0o755))

	entries := []links.Entry{
		{Pattern: "config/local*.yaml"},
		{Pattern: ".env"},
		{Pattern: ".env.*"},
		{Pattern: ".idea/runConfigurations", Dir: true},
	}

	actions, err := links.Apply(mainDir, wt, entries)
	require.NoError(t, err)

	assert.Equal(t, map[string]links.Status{
		"config/local.yaml":         links.Linked,
		"config/local.staging.yaml": links.Linked,
		".env":                      links.Missing,
		".env.example":              links.Skipped,
		".idea/runConfigurations":   links.Linked,
	}, statuses(actions))

	assert.DirExists(t, filepath.Join(mainDir, ".idea", "runConfigurations"), "directory entries are created in main/")

	content, err := os.ReadFile(filepath.Join(wt, "config", "local.staging.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "b", string(content))

	info, err := os.Lstat(filepath.Join(wt, ".env.example"))
	require.NoError(t, err)
	assert.Zero(t, info.Mode()&os.ModeSymlink, "an existing file must not be replaced")

	again, err := links.Apply(mainDir, wt, entries)
	require.NoError(t, err)
	assert.Equal(t, links.Present, statuses(again)["config/local.yaml"], "applying twice is a no-op")
}

func TestSeed(t *testing.T) {
	dir := t.TempDir()
	from, mainDir := filepath.Join(dir, "old"), filepath.Join(dir, "main")
	write(t, filepath.Join(from, "config", "local.yaml"), "old")
	write(t, filepath.Join(from, ".idea", "runConfigurations", "a.xml"), "<a/>")
	write(t, filepath.Join(mainDir, "config", "local.staging.yaml"), "keep")
	write(t, filepath.Join(from, "config", "local.staging.yaml"), "overwrite?")

	actions, err := links.Seed(from, mainDir, []links.Entry{
		{Pattern: "config/local*.yaml"},
		{Pattern: ".idea/runConfigurations", Dir: true},
		{Pattern: ".env"},
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]links.Status{
		"config/local.yaml":         links.Copied,
		"config/local.staging.yaml": links.Present,
		".idea/runConfigurations":   links.Copied,
		".env":                      links.Missing,
	}, statuses(actions))

	for path, want := range map[string]string{
		"config/local.yaml":             "old",
		"config/local.staging.yaml":     "keep",
		".idea/runConfigurations/a.xml": "<a/>",
	} {
		got, err := os.ReadFile(filepath.Join(mainDir, path))
		require.NoError(t, err, path)
		assert.Equal(t, want, string(got), path)
	}
}

func container(t *testing.T, content string) string {
	t.Helper()

	dir := t.TempDir()
	if content != "" {
		write(t, filepath.Join(dir, "wt.yml"), content)
	}

	return dir
}

func entries(t *testing.T, paths ...string) []links.Entry {
	t.Helper()

	e, err := links.ParseEntries(paths)
	require.NoError(t, err)

	return e
}

func readConfig(t *testing.T, dir string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(dir, "wt.yml"))
	require.NoError(t, err)

	return string(raw)
}

func TestAddToFile_CreatesFromTemplate(t *testing.T) {
	dir := container(t, "")

	added, err := links.AddToFile(dir, entries(t, ".env", ".idea/runConfigurations/"))
	require.NoError(t, err)
	assert.Len(t, added, 2)

	raw := readConfig(t, dir)
	assert.True(t, strings.HasPrefix(raw, config.Template), "the explanatory comments come along")
	assert.True(t, strings.HasSuffix(raw, "\nlinks:\n  - .env\n  - .idea/runConfigurations/\n"), raw)
}

func TestAddToFile_KeepsExistingItemsAndSkipsListed(t *testing.T) {
	dir := container(t, "# mine\n\nide: vscode # not goland\nlinks:\n  # secrets\n  - .env\n  - config/local*.yaml\n")

	added, err := links.AddToFile(dir, entries(t, ".env", "docker/", "docker"))
	require.NoError(t, err)
	assert.Equal(t, entries(t, "docker/"), added, "listed paths and duplicates are not added again")

	assert.Equal(t, "# mine\n\nide: vscode # not goland\nlinks:\n  # secrets\n  - .env\n  - config/local*.yaml\n  - docker/\n", readConfig(t, dir))

	added, err = links.AddToFile(dir, entries(t, "docker"))
	require.NoError(t, err)
	assert.Empty(t, added)
}

func TestAddToFile_QuotesWhatYAMLWouldMisread(t *testing.T) {
	dir := container(t, "")

	_, err := links.AddToFile(dir, entries(t, "*.local", "!important", "a: b", "#hash"))
	require.NoError(t, err)

	got, err := links.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, entries(t, "*.local", "!important", "a: b", "#hash"), got)
}

func TestRemoveFromFile(t *testing.T) {
	dir := container(t, "# keep me\n\nlinks:\n  - .env # secrets\n  - '  config/local*.yaml  '\n  - .idea/runConfigurations/\n")

	removed, err := links.RemoveFromFile(dir, entries(t, "config/local*.yaml", ".idea/runConfigurations"))
	require.NoError(t, err)
	assert.Equal(t, entries(t, "config/local*.yaml", ".idea/runConfigurations/"), removed, "as they were listed")

	assert.Equal(t, "# keep me\n\nlinks:\n  - .env # secrets\n", readConfig(t, dir))
}

func TestRemoveFromFile_RemovingTheLastOneKeepsTheRest(t *testing.T) {
	dir := container(t, "# keep me\n\nide: vscode\nlinks:\n  - .env\n")

	_, err := links.RemoveFromFile(dir, entries(t, ".env"))
	require.NoError(t, err)
	assert.Equal(t, "# keep me\n\nide: vscode\n", readConfig(t, dir))

	dir = container(t, "links:\n  - .env\n")
	_, err = links.RemoveFromFile(dir, entries(t, ".env"))
	require.NoError(t, err)
	assert.Equal(t, "", readConfig(t, dir), "wt.yml stays, it is what marks the repository as managed")
}

func TestRemoveFromFile_ChangesNothingIfAnyPathIsNotListed(t *testing.T) {
	const content = "links:\n  - .env\n  - config/local.yaml\n"
	dir := container(t, content)

	_, err := links.RemoveFromFile(dir, entries(t, ".env", "typo.yaml"))

	var notListed *links.NotListedError
	require.ErrorAs(t, err, &notListed)
	assert.Equal(t, []string{"typo.yaml"}, notListed.Patterns)
	assert.Equal(t, content, readConfig(t, dir))

	_, err = links.RemoveFromFile(container(t, ""), entries(t, ".env"))
	assert.Error(t, err, "no wt.yml means nothing is listed")
}

func TestUnlink(t *testing.T) {
	dir := t.TempDir()
	mainDir, wt := filepath.Join(dir, "main"), filepath.Join(dir, "wt")
	write(t, filepath.Join(mainDir, "config", "local.yaml"), "a")
	write(t, filepath.Join(mainDir, "config", "local.staging.yaml"), "b")
	write(t, filepath.Join(mainDir, ".env.example"), "tracked")
	write(t, filepath.Join(wt, ".env.example"), "tracked")
	write(t, filepath.Join(wt, "keep", "file.txt"), "x")

	all := entries(t, "config/local*.yaml", ".env.*", ".idea/runConfigurations/")
	_, err := links.Apply(mainDir, wt, all)
	require.NoError(t, err)
	write(t, filepath.Join(wt, ".idea", "runConfigurations", "server.xml"), "<xml/>")

	// A link whose target was deleted is still ours to remove.
	require.NoError(t, os.Remove(filepath.Join(mainDir, "config", "local.staging.yaml")))

	actions, err := links.Unlink(mainDir, wt, all)
	require.NoError(t, err)

	assert.Equal(t, map[string]links.Status{
		"config/local.yaml":         links.Unlinked,
		"config/local.staging.yaml": links.Unlinked,
		".idea/runConfigurations":   links.Unlinked,
	}, statuses(actions), "the tracked .env.example is not a link into main/ and is left alone")

	assert.NoDirExists(t, filepath.Join(wt, "config"), "the directory Apply created is removed once empty")
	assert.FileExists(t, filepath.Join(wt, ".env.example"))
	assert.FileExists(t, filepath.Join(wt, "keep", "file.txt"))

	// Unlinking never reaches through a link into main/.
	assert.FileExists(t, filepath.Join(mainDir, "config", "local.yaml"))
	assert.FileExists(t, filepath.Join(mainDir, ".idea", "runConfigurations", "server.xml"))

	again, err := links.Unlink(mainDir, wt, all)
	require.NoError(t, err)
	assert.Empty(t, again, "unlinking twice is a no-op")
}

func TestUnlink_LeavesLinksToElsewhereAndRealFiles(t *testing.T) {
	dir := t.TempDir()
	mainDir, wt := filepath.Join(dir, "main"), filepath.Join(dir, "wt")
	write(t, filepath.Join(mainDir, ".env"), "main")
	write(t, filepath.Join(wt, ".env"), "own copy")
	write(t, filepath.Join(dir, "other"), "x")
	require.NoError(t, os.Symlink("../other", filepath.Join(wt, "config.yaml")))

	actions, err := links.Unlink(mainDir, wt, entries(t, ".env", "config.yaml"))
	require.NoError(t, err)

	assert.Empty(t, actions)
	assert.FileExists(t, filepath.Join(wt, ".env"))
	assert.FileExists(t, filepath.Join(dir, "other"))
	_, err = os.Lstat(filepath.Join(wt, "config.yaml"))
	assert.NoError(t, err)
}
