// Package cli maps cobra commands onto the manager package and renders its results.
package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/manager"
)

// Execute runs the wt command tree.
func Execute(ctx context.Context) error {
	return newRoot().ExecuteContext(ctx)
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "wt",
		Short: "Work on a repository through git worktrees, one Claude session each",
		Long: `wt keeps one read-only clone per repository and gives every task its own worktree,
branch and Claude Code session.

  <root>/<org>/<repo>/main/           the clone; never edited
  <root>/<org>/<repo>/worktrees/<slug>/
  <root>/<org>/<repo>/wt.links        gitignored paths symlinked from main/ into each worktree (none by default)
  <root>/<org>/<repo>/wt.conf         per-repository settings, e.g. the IDE

Run 'wt ui' for an interactive list of worktrees.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newCloneCmd(),
		newInitCmd(),
		newLinkCmd(),
		newIDECmd(),
		newUICmd(),
		newNewCmd(),
		newListCmd(),
		newRemoveCmd(),
		newPathCmd(),
		newClaudeCmd(),
		newOpenCmd(),
		newGuardCmd(),
	)

	return root
}

// managerHere finds the managed repository containing the working directory.
func managerHere(cmd *cobra.Command) (manager.Manager, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return manager.Manager{}, err
	}

	l, err := layout.Discover(cmd.Context(), cwd)
	if err != nil {
		return manager.Manager{}, err
	}

	return manager.Manager{Layout: l}, nil
}

// slugCompletion completes worktree slugs for commands that take one.
func slugCompletion(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	m, err := managerHere(cmd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	infos, err := m.List(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	slugs := make([]string, 0, len(infos))
	for _, info := range infos {
		slugs = append(slugs, info.Slug)
	}

	return slugs, cobra.ShellCompDirectiveNoFileComp
}
