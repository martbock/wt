// Package ide knows the IDEs that wt can open a worktree in, and writes the small bits of
// metadata that make worktrees recognizable in JetBrains IDEs.
package ide

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NameFile is the file JetBrains IDEs read the project name from.
const NameFile = ".idea/.name"

// AppEnv names the IDE (a preset or a macOS application) to use in repositories whose wt.yml
// does not choose one.
const AppEnv = "WT_IDE_APP"

// IDE is an editor that worktrees can be opened in.
type IDE struct {
	// Name is the short name used in wt.yml and on the command line, e.g. "vscode".
	Name string

	// App is the macOS application that 'open -a' starts.
	App string

	// JetBrains is true for IDEs that read the project name from .idea/.name.
	JetBrains bool
}

var presets = []IDE{
	{Name: "goland", App: "GoLand", JetBrains: true},
	{Name: "webstorm", App: "WebStorm", JetBrains: true},
	{Name: "idea", App: "IntelliJ IDEA", JetBrains: true},
	{Name: "pycharm", App: "PyCharm", JetBrains: true},
	{Name: "vscode", App: "Visual Studio Code"},
	{Name: "cursor", App: "Cursor"},
}

// Default is the IDE of a repository that does not choose one: $WT_IDE_APP, else GoLand.
func Default() IDE {
	if v := strings.TrimSpace(os.Getenv(AppEnv)); v != "" {
		return Resolve(v)
	}

	return presets[0]
}

// Resolve turns what a user wrote into an IDE: a preset name such as "vscode", the application
// name of a preset such as "Visual Studio Code" (both ignoring case), or else any macOS
// application name, which is opened as it is.
func Resolve(name string) IDE {
	name = strings.TrimSpace(name)

	for _, p := range presets {
		if strings.EqualFold(name, p.Name) || strings.EqualFold(name, p.App) {
			return p
		}
	}

	return IDE{Name: name, App: name}
}

// Known reports whether the IDE is one of the presets.
func (i IDE) Known() bool {
	for _, p := range presets {
		if p.Name == i.Name {
			return true
		}
	}

	return false
}

// Names lists the preset names.
func Names() []string {
	names := make([]string, len(presets))
	for i, p := range presets {
		names[i] = p.Name
	}

	return names
}

// Open opens the project at path in the IDE.
func (i IDE) Open(ctx context.Context, path string) error {
	return exec.CommandContext(ctx, "open", "-a", i.App, path).Run()
}

// WriteName sets the display name a JetBrains IDE shows for the project rooted at dir.
func WriteName(dir, name string) error {
	path := filepath.Join(dir, NameFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(name), 0o644)
}
