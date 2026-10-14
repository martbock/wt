package cli

import (
	"github.com/spf13/cobra"

	"github.com/martbock/wt/internal/tui"
)

func newUICmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ui",
		Aliases: []string{"tui"},
		Short:   "Interactive mode: browse, enter, create and remove worktrees",
		Long: `Full-screen list of this repository's worktrees.

  enter  start a Claude session in the worktree     c  continue its last session
  o      open it in the IDE (see 'wt ide')          n  new worktree: [slug] [--prefix P] [--branch B]
  d      remove it (and its branch, if safe)        l  shared files (wt.links): add, remove, sync
  r      refresh                                    q  quit

A Claude session takes over the terminal; when it ends you are back in the list.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			return tui.Run(cmd.Context(), m)
		},
	}
}
