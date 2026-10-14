package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/martbock/wt/internal/config"
	"github.com/martbock/wt/internal/ide"
)

func newGlobalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "global",
		Short: "Show or change the settings that apply to every repository",
		Long: `Settings that apply to every repository are kept in ~/.config/wt/config.yml (under
$XDG_CONFIG_HOME when that is set), for example "ide: vscode". A repository's own wt.yml wins
over them.

Settings: ` + strings.Join(config.GlobalKeys(), ", ") + `
  ide   the IDE 'wt open' uses where a repository does not choose one: ` + strings.Join(ide.Names(), ", ") + `,
        or a macOS application name. $` + ide.AppEnv + ` still wins over it; see 'wt ide'.`,
		Example: `  wt global
  wt global set ide vscode
  wt global unset ide`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.GlobalFile()
			if err != nil {
				return err
			}

			cfg, err := config.LoadGlobal()
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%s\n", path)
			for _, key := range config.GlobalKeys() {
				value := cfg.Get(key)
				if value == "" {
					value = "(not set)"
				}

				fmt.Fprintf(w, "  %-4s %s\n", key, value)
			}

			return nil
		},
	}

	cmd.AddCommand(newGlobalSetCmd(), newGlobalUnsetCmd())

	return cmd
}

func newGlobalSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Set a global setting",
		Args:      cobra.ExactArgs(2),
		ValidArgs: config.GlobalKeys(),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], strings.TrimSpace(args[1])
			if err := checkGlobalKey(key); err != nil {
				return err
			}
			if value == "" {
				return fmt.Errorf("empty value (use 'wt global unset %s' to remove the setting)", key)
			}

			if err := config.SetGlobal(key, value); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\n", key, value)

			return nil
		},
	}
}

func newGlobalUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "unset <key>",
		Short:     "Remove a global setting",
		Args:      cobra.ExactArgs(1),
		ValidArgs: config.GlobalKeys(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkGlobalKey(args[0]); err != nil {
				return err
			}

			if err := config.SetGlobal(args[0], ""); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s unset\n", args[0])

			return nil
		},
	}
}

func checkGlobalKey(key string) error {
	if !slices.Contains(config.GlobalKeys(), key) {
		return fmt.Errorf("unknown setting %q: use one of %s", key, strings.Join(config.GlobalKeys(), ", "))
	}

	return nil
}
