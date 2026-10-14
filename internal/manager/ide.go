package manager

import (
	"context"
	"os"
	"strings"

	"github.com/martbock/wt/internal/config"
	"github.com/martbock/wt/internal/ide"
)

// IDESource says where the IDE of a repository was chosen.
type IDESource string

const (
	IDEFromConfig  IDESource = "wt.yml"
	IDEFromEnv     IDESource = "$" + ide.AppEnv
	IDEFromGlobal  IDESource = "global config"
	IDEFromDefault IDESource = "default"
)

// ChosenIDE is the IDE of a repository and why it is that one.
type ChosenIDE struct {
	IDE    ide.IDE
	Source IDESource
}

// IDE returns the IDE for this repository: the one in wt.yml, else $WT_IDE_APP, else the one in
// the global config, else GoLand.
func (m Manager) IDE() (ChosenIDE, error) {
	cfg, err := config.Load(m.Layout.Container)
	if err != nil {
		return ChosenIDE{}, err
	}

	global, err := config.LoadGlobal()
	if err != nil {
		return ChosenIDE{}, err
	}

	switch {
	case cfg.IDE != "":
		return ChosenIDE{IDE: ide.Resolve(cfg.IDE), Source: IDEFromConfig}, nil
	case strings.TrimSpace(os.Getenv(ide.AppEnv)) != "":
		return ChosenIDE{IDE: ide.Default(), Source: IDEFromEnv}, nil
	case global.IDE != "":
		return ChosenIDE{IDE: ide.Resolve(global.IDE), Source: IDEFromGlobal}, nil
	default:
		return ChosenIDE{IDE: ide.Default(), Source: IDEFromDefault}, nil
	}
}

// SetIDE makes name the IDE of this repository, or removes the choice if name is empty. Worktrees
// that already exist keep what was written into them; nothing is moved or deleted.
func (m Manager) SetIDE(name string) (ChosenIDE, error) {
	if err := config.SetIDE(m.Layout.Container, name); err != nil {
		return ChosenIDE{}, err
	}

	return m.IDE()
}

// OpenInIDE opens the directory in this repository's IDE and returns the IDE it used.
func (m Manager) OpenInIDE(ctx context.Context, path string) (ide.IDE, error) {
	chosen, err := m.IDE()
	if err != nil {
		return ide.IDE{}, err
	}

	return chosen.IDE, chosen.IDE.Open(ctx, path)
}
