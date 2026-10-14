package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/manager"
)

func newIDECmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ide",
		Short: "Show or change the IDE that 'wt open' uses for this repository",
		Long: `Each repository has its own IDE, so Go repositories can open in GoLand and Node ones in VS Code.
It is stored in wt.conf next to wt.links. Without a setting, $` + ide.AppEnv + ` applies, then GoLand.

Presets: ` + strings.Join(ide.Names(), ", ") + `. Any other value is taken as a macOS application name,
as in 'open -a <name>'.

Only JetBrains IDEs get a project name (.idea/.name) written into the main clone and new worktrees.`,
		Example: `  wt ide
  wt ide set vscode
  wt ide set "Sublime Text"
  wt ide unset`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			chosen, err := m.IDE()
			if err != nil {
				return err
			}

			renderIDE(cmd, chosen)

			return nil
		},
	}

	cmd.AddCommand(newIDESetCmd(), newIDEUnsetCmd())

	return cmd
}

func newIDESetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <ide>",
		Short: "Use <ide> for this repository",
		Long: `Use <ide> for this repository: ` + strings.Join(ide.Names(), ", ") + `, or a macOS application name.
Worktrees that already exist keep the project name they have; nothing is moved or deleted.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: ide.Names(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return fmt.Errorf("empty IDE name (use 'wt ide unset' to remove the setting)")
			}

			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			return setIDE(cmd, m, args[0])
		},
	}
}

func newIDEUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset",
		Short: "Remove this repository's IDE setting, falling back to $" + ide.AppEnv + " or GoLand",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			return setIDE(cmd, m, "")
		},
	}
}

func setIDE(cmd *cobra.Command, m manager.Manager, name string) error {
	chosen, err := m.SetIDE(name)
	if err != nil {
		return err
	}

	renderIDE(cmd, chosen)

	return nil
}

func renderIDE(cmd *cobra.Command, c manager.ChosenIDE) {
	w := cmd.OutOrStdout()

	fmt.Fprintf(w, "%s (%s)\n", c.IDE.App, c.Source)
	if !c.IDE.Known() {
		fmt.Fprintf(w, "Not a preset: 'wt open' runs: open -a %q <worktree>\n", c.IDE.App)
	}
}
