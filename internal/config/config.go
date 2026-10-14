// Package config reads and writes wt's settings files, both YAML: wt.yml, the per-repository
// settings in the container next to main/, and config.yml in the user's config directory, whose
// settings apply to every repository.
//
// Writing keeps the comments and the keys wt does not know, so the files can be edited by hand.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/martbock/wt/internal/layout"
)

// Settings. KeyLinks is only meaningful in a repository's wt.yml.
const (
	KeyIDE   = "ide"
	KeyLinks = "links"
)

// GlobalFileName is the name of the global settings file in wt's config directory.
const GlobalFileName = "config.yml"

// Template is the contents of a freshly created wt.yml. It sets nothing: which files are worth
// sharing depends on the repository, so every repository opts in.
const Template = `# Settings for this repository. Most are managed with commands, but editing the file is fine.

# The IDE that 'wt open' uses: goland, webstorm, idea, pycharm, vscode, cursor or a macOS
# application name. Without it, $WT_IDE_APP applies, then the global one ('wt global'), then GoLand.
#
# ide: goland

# Paths relative to main/ that every worktree symlinks back to main/. Nothing is linked until it
# is listed here. Manage the list with 'wt link add|rm', or edit it and run 'wt link sync'.
#
# Globs are allowed. A trailing slash marks a directory: it is created in main/ if missing, so
# files the IDE writes into it from a worktree land in main/ too.
#
# links:
#   - config/local*.yaml
#   - .env
#   - .idea/runConfigurations/
`

// GlobalKeys lists the settings 'wt global' can change.
func GlobalKeys() []string { return []string{KeyIDE} }

// Config holds the settings of one file. A zero value means "not set".
type Config struct {
	// IDE is the preset name or macOS application that 'wt open' uses.
	IDE string `yaml:"ide"`

	// Links are the paths, relative to main/, that worktrees symlink back to main/.
	Links []string `yaml:"links"`
}

// Get returns the value of a single-valued setting, empty when it is not set.
func (c Config) Get(key string) string {
	if key == KeyIDE {
		return c.IDE
	}

	return ""
}

// File is the path of a repository's settings file.
func File(container string) string {
	return filepath.Join(container, layout.ConfigFile)
}

// Load reads wt.yml from the container. A missing file means nothing is set. Keys it does not
// know are ignored, so a newer wt.yml still works with an older wt.
func Load(container string) (Config, error) {
	return loadFile(File(container))
}

// Ensure creates wt.yml from Template if the container has none, and reports whether it did.
func Ensure(container string) (created bool, err error) {
	path := File(container)
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	return true, os.WriteFile(path, []byte(Template), 0o644)
}

// SetIDE writes the ide setting of a repository. An empty name removes it.
func SetIDE(container, name string) error {
	return edit(File(container), Template, false, func(d *doc) error {
		d.setString(KeyIDE, name)
		return nil
	})
}

// EditLinks hands the paths listed under links in wt.yml to fn and writes back what it returns,
// keeping the other settings and the comments. A missing wt.yml starts from Template. Nothing is
// written when fn fails or returns the list unchanged.
func EditLinks(container string, fn func(current []string) ([]string, error)) error {
	return edit(File(container), Template, false, func(d *doc) error {
		current, err := d.list(KeyLinks)
		if err != nil {
			return err
		}

		updated, err := fn(slices.Clone(current))
		if err != nil {
			return err
		}
		if !slices.Equal(current, updated) {
			d.setList(KeyLinks, updated)
		}

		return nil
	})
}

// GlobalFile is the path of the global settings file: $XDG_CONFIG_HOME/wt/config.yml, which is
// ~/.config/wt/config.yml unless $XDG_CONFIG_HOME says otherwise.
func GlobalFile() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}

	return filepath.Join(dir, "wt", GlobalFileName), nil
}

// LoadGlobal reads the global settings file. A missing file means nothing is set.
func LoadGlobal() (Config, error) {
	path, err := GlobalFile()
	if err != nil {
		return Config{}, err
	}

	return loadFile(path)
}

// SetGlobal writes a setting to the global settings file, creating it and its directory as
// needed. An empty value removes the setting, and the file when nothing is left in it.
func SetGlobal(key, value string) error {
	path, err := GlobalFile()
	if err != nil {
		return err
	}

	return edit(path, "", true, func(d *doc) error {
		d.setString(key, value)
		return nil
	})
}

func loadFile(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}

	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	c.IDE = strings.TrimSpace(c.IDE)

	return c, nil
}
