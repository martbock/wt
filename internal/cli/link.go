package cli

import (
	"github.com/spf13/cobra"
)

func newLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Show or change which files worktrees share with main/",
		Long: `Worktrees symlink the paths listed under links in wt.yml back to main/, so a gitignored file such as
config/local.yaml exists once and is edited everywhere. Nothing is listed by default.

add and rm update wt.yml and apply the change to every existing worktree, not only to new
ones. Paths are relative to main/; globs are allowed and a trailing / marks a directory.`,
		Example: `  wt link ls
  wt link add 'config/local*.yaml' .env
  wt link add .idea/runConfigurations/
  wt link rm .env`,
	}

	cmd.AddCommand(newLinkLsCmd(), newLinkAddCmd(), newLinkRmCmd(), newLinkSyncCmd())

	return cmd
}

func newLinkLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the paths shared with every worktree",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			infos, err := m.Links()
			if err != nil {
				return err
			}

			renderLinks(cmd.OutOrStdout(), infos)

			return nil
		},
	}
}

func newLinkAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <path>...",
		Short: "Share paths with every worktree, existing ones included",
		Long: `Add paths (relative to main/, globs allowed, trailing / for a directory) to the links in wt.yml and symlink
them into every existing worktree. A path that is already listed is applied again, which repairs
worktrees that lack the link. A file that a worktree already has is left alone.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			change, err := m.AddLinks(cmd.Context(), args)
			renderLinkChange(cmd.OutOrStdout(), "Added to", change)

			return err
		},
	}
}

func newLinkRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <path>...",
		Aliases: []string{"remove"},
		Short:   "Stop sharing paths: unlink them from every worktree",
		Long: `Remove paths from the links in wt.yml and delete their symlinks from every existing worktree. The files in
main/ stay, and so does anything in a worktree that is not a link into main/. A path must be
listed (see 'wt link ls'); if one is not, nothing is changed.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			change, err := m.RemoveLinks(cmd.Context(), args)
			renderLinkChange(cmd.OutOrStdout(), "Removed from", change)

			return err
		},
	}
}

func newLinkSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Apply the links in wt.yml to every existing worktree (after editing the file by hand)",
		Long: `Symlink everything the links in wt.yml list into every existing worktree. It only adds links; to stop
sharing a path use 'wt link rm'.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			change, err := m.SyncLinks(cmd.Context())
			renderLinkChange(cmd.OutOrStdout(), "", change)

			return err
		},
	}
}
