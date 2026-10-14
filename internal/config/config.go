// Package config reads and writes wt.conf, the per-repository settings that live in the
// container next to wt.links. The format is one "key = value" per line; "#" starts a comment.
package config

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/martbock/wt/internal/layout"
)

const keyIDE = "ide"

// Config holds a repository's settings. A zero value means "not set".
type Config struct {
	// IDE is the preset name or macOS application that 'wt open' uses for this repository.
	IDE string
}

// Load reads wt.conf from the container. A missing file means nothing is set. Keys it does not
// know are ignored, so a newer wt.conf still works with an older wt.
func Load(container string) (Config, error) {
	f, err := os.Open(filepath.Join(container, layout.ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	defer func() { _ = f.Close() }()

	var c Config
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if key, value, ok := parseLine(scanner.Text()); ok && key == keyIDE {
			c.IDE = value
		}
	}

	return c, scanner.Err()
}

// SetIDE writes the ide setting, keeping every other line. An empty name removes the setting.
func SetIDE(container, name string) error {
	path := filepath.Join(container, layout.ConfigFile)

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	name = strings.TrimSpace(name)

	var b strings.Builder
	written := false
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		key, _, ok := parseLine(line)
		if !ok || key != keyIDE {
			b.WriteString(line)
			continue
		}

		// Replace the first setting; drop any repeats.
		if name != "" && !written {
			b.WriteString(keyIDE + " = " + name + "\n")
			written = true
		}
	}

	if name != "" && !written {
		out := b.String()
		if out != "" && !strings.HasSuffix(out, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(keyIDE + " = " + name + "\n")
	}

	if strings.TrimSpace(b.String()) == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		return nil
	}

	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}

	return strings.TrimSpace(key), strings.TrimSpace(value), true
}
