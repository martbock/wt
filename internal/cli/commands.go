package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/martbock/wt/internal/guard"
	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/manager"
	"github.com/martbock/wt/internal/session"
)

func newCloneCmd() *cobra.Command {
	var seed, ideName string
	var linkPaths []string

	cmd := &cobra.Command{
		Use:   "clone <url> [<directory>]",
		Short: "Clone a repository into <directory>/main and set it up for worktrees",
		Long: `Clone a repository into <directory>/main, write wt.yml, name the IDE project and install a
pre-commit hook that refuses commits in main/. Like git clone, <directory> is relative to the
current directory and defaults to the repository name.

No files are shared between worktrees unless you ask for them: --link lists a path (relative to
main/, globs allowed, a trailing / for a directory) that every worktree symlinks back to main/.
Repeat it for several paths. Change the list later with 'wt link add' and 'wt link rm'.

--ide picks the IDE that 'wt open' uses for this repository; change it later with 'wt ide set'.

--seed copies the --link paths (local config, .env, ...) from an existing clone, so they do not
have to be recreated by hand.`,
		Example: `  wt clone git@github.com:acme/api.git              creates ./api/main
  wt clone git@github.com:acme/api.git ~/git/acme/api
  wt clone git@github.com:acme/api.git --link 'config/local*.yaml' --link .env \
      --seed ~/git/old/api`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := manager.CloneOptions{URL: args[0], Links: linkPaths, IDE: ideName, Seed: expandHome(seed)}
			if len(args) == 2 {
				opts.Dir = args[1]
			}

			res, err := manager.Clone(cmd.Context(), opts)
			if res.Layout.Main != "" {
				renderInit(cmd.OutOrStdout(), res.Layout, res.Init, res.Seeded)
			}

			return err
		},
	}

	cmd.Flags().StringArrayVar(&linkPaths, "link", nil, "path in main/ to symlink into every worktree (repeatable; default: none)")
	cmd.Flags().StringVar(&ideName, "ide", "", "IDE for this repository: "+strings.Join(ide.Names(), ", ")+", or a macOS app name (default: $"+ide.AppEnv+", the global IDE or goland)")
	cmd.Flags().StringVar(&seed, "seed", "", "existing clone to copy the --link paths from")

	return cmd
}

func newConvertCmd() *cobra.Command {
	var ideName string
	var linkPaths []string

	cmd := &cobra.Command{
		Use:   "convert [<directory>]",
		Short: "Turn an existing clone into <directory>/main and set it up for worktrees",
		Long: `Turn a directory with a clone directly in it into the wt layout, in place: the clone moves to
<directory>/main and <directory> becomes the container, so the repository keeps its name and place.
Nothing inside the clone changes. Uncommitted work, stashes and gitignored files such as local
config come along. Then it does what 'wt init' does: write wt.yml, name the IDE project and install
the pre-commit hook that refuses commits in main/.

<directory> defaults to the current directory. Your shell stays at the old path, which is now the
container; 'cd main' (or 'cd <directory>/main') to get back into the clone.

wt expects main/ to stay on the default branch and clean. Convert does not change the branch or
discard anything, but it tells you when the clone is not like that.

A clone with linked worktrees is refused, because they would lose track of it: remove them first.
--link and --ide work as they do for 'wt clone'.`,
		Example: `  wt convert ~/git/acme/api
  cd ~/git/acme/api && wt convert --link .env --link 'config/local*.yaml'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := manager.ConvertOptions{Dir: ".", Links: linkPaths, IDE: ideName}
			if len(args) == 1 {
				opts.Dir = expandHome(args[0])
			}

			inside := containsCwd(opts.Dir)

			res, err := manager.Convert(cmd.Context(), opts)
			if res.Layout.Main != "" {
				renderConverted(cmd.OutOrStdout(), res, inside)
			}

			return err
		},
	}

	cmd.Flags().StringArrayVar(&linkPaths, "link", nil, "path in main/ to symlink into every worktree (repeatable; default: none)")
	cmd.Flags().StringVar(&ideName, "ide", "", "IDE for this repository: "+strings.Join(ide.Names(), ", ")+", or a macOS app name (default: $"+ide.AppEnv+", the global IDE or goland)")

	return cmd
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Set up an existing <container>/main clone for worktrees (safe to repeat)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			res, err := m.Init(cmd.Context())
			renderInit(cmd.OutOrStdout(), m.Layout, res, nil)

			return err
		},
	}
}

func newNewCmd() *cobra.Command {
	var prefix, branch, base string
	var noFetch, launch bool

	cmd := &cobra.Command{
		Use:   "new [slug]",
		Short: "Create a worktree named <slug> on a new or existing branch, e.g. 'wt new fix-login-bug'",
		Long: `Create a worktree at <container>/worktrees/<slug> on a new branch <slug>, cut from origin's
default branch. Paths listed under links in wt.yml (see 'wt link') are symlinked in from main/.

--prefix goes in front of the branch name, not the directory. It is used as typed, except that a
bare ticket ID gets its dash:

  wt new fix-login-bug                      worktree and branch: fix-login-bug
  wt new login-bug --prefix fix/            worktree: login-bug, branch: fix/login-bug
  wt new login-bug --prefix PROJ-1234       worktree: login-bug, branch: PROJ-1234-login-bug

--branch works on a named branch instead, e.g. to review a pull request. It is checked out if it
exists locally, tracked from origin if it exists there, and created from --base otherwise. The slug
is derived from the branch name unless you give one:

  wt new --branch feat/PROJ-1234-login      worktree: feat-proj-1234-login
  wt new review-1234 --branch feat/login    worktree: review-1234`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := manager.CreateOptions{Prefix: prefix, Branch: branch, Base: base, SkipFetch: noFetch}
			if len(args) > 0 {
				opts.Slug = args[0]
			}

			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			created, err := m.Create(cmd.Context(), opts)
			if created.Path != "" {
				renderCreated(cmd.OutOrStdout(), created)
			}
			if err != nil {
				return err
			}

			if launch {
				return execClaude(created.Path, m.Layout.Repo, created.Slug, false, nil)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&prefix, "prefix", "", "put this in front of the new branch name, e.g. fix/ or PROJ-1234")
	cmd.Flags().StringVar(&branch, "branch", "", "work on this branch: check it out if it exists, create it otherwise")
	cmd.Flags().StringVar(&base, "base", "", "ref to create a new branch from (default: origin's default branch)")
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "do not fetch origin first")
	cmd.Flags().BoolVar(&launch, "claude", false, "start a Claude session in the new worktree")
	cmd.MarkFlagsMutuallyExclusive("prefix", "branch")

	return cmd
}

func newListCmd() *cobra.Command {
	var withPR bool

	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List worktrees and whether they are merged, with their pull request on request",
		Long: `List worktrees: branch, commits ahead of origin's default branch, uncommitted changes and whether
its work is merged. --pr adds the branch's pull request, looked up with the GitHub CLI.

MERGED says yes when every commit is in the default branch (also after a rebase merge) or the
pull request is merged, no commits when the branch has nothing of its own, and remote gone when
the branch was pushed and has since been deleted on the remote, as after a squash merge. It is as
fresh as your last fetch; 'wt new' and 'wt rm' fetch, 'git fetch' does too.

The pull request lookup asks GitHub and takes a moment, so it is off by default. With --pr but
without 'gh' the PR column shows ? and the rest still works.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			infos, err := m.List(cmd.Context())
			if err != nil {
				return err
			}

			var prs *manager.PRs
			if withPR && len(infos) > 0 {
				found := m.PullRequests(cmd.Context(), infos)
				prs = &found
			}

			renderList(cmd.OutOrStdout(), infos, prs)

			return nil
		},
	}

	cmd.Flags().BoolVar(&withPR, "pr", false, "look up each branch's pull request with gh (slower)")

	return cmd
}

func newRemoveCmd() *cobra.Command {
	var force, noFetch bool

	cmd := &cobra.Command{
		Use:     "rm <slug>",
		Aliases: []string{"remove"},
		Short:   "Remove a worktree, and its branch if no commits would be lost",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			res, err := m.Remove(cmd.Context(), args[0], manager.RemoveOptions{Force: force, SkipFetch: noFetch})
			if err != nil {
				return err
			}

			renderRemoved(cmd.OutOrStdout(), res)

			return nil
		},
		ValidArgsFunction: slugCompletion,
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even with uncommitted or untracked changes")
	cmd.Flags().BoolVar(&noFetch, "no-fetch", false, "do not fetch origin first")

	return cmd
}

func newPurgeCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "purge",
		Short: "Remove every clean worktree whose pull request is merged, and its branch",
		Long: `Remove the worktrees that are finished: no uncommitted or untracked changes, and the branch is
exactly at the tip of a merged pull request, looked up with the GitHub CLI. Their branches are
deleted too, since the merged pull request has everything on them.

A worktree is left alone when it has changes, when it has commits made after the merge, or when
its branch has no merged pull request. Nothing is removed if the lookup fails (no 'gh', not logged
in, offline).

--dry-run lists what would be removed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			infos, err := m.List(cmd.Context())
			if err != nil {
				return err
			}

			prs := m.PullRequests(cmd.Context(), infos)
			if prs.Err != nil {
				return fmt.Errorf("cannot look up pull requests: %w", prs.Err)
			}

			candidates := manager.PurgeCandidates(infos, prs)
			if dryRun {
				renderPurgePlan(cmd.OutOrStdout(), candidates)
				return nil
			}

			slugs := make([]string, len(candidates))
			for i, c := range candidates {
				slugs[i] = c.Slug
			}

			return renderPurged(cmd.OutOrStdout(), m.Purge(cmd.Context(), slugs))
		},
	}

	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "list what would be removed, remove nothing")

	return cmd
}

func newPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "path <slug>",
		Short:             "Print a worktree's path, e.g. cd \"$(wt path fix-login-bug)\"",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: slugCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			info, err := m.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), info.Path)

			return nil
		},
	}
}

func newClaudeCmd() *cobra.Command {
	var resume bool

	cmd := &cobra.Command{
		Use:   "claude <slug> [-- claude flags]",
		Short: "Start a Claude Code session in a worktree",
		Long: `Start Claude Code in the worktree, named "<repo>: <slug>" so it is easy to find in /resume.
With --continue, pick up the worktree's most recent session instead. Anything after -- is passed
on to claude.`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: slugCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			slug, extra := args[0], args[1:]

			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			info, err := m.Find(cmd.Context(), slug)
			if err != nil {
				return err
			}

			return execClaude(info.Path, m.Layout.Repo, slug, resume, extra)
		},
	}

	cmd.Flags().BoolVarP(&resume, "continue", "c", false, "continue the most recent session in the worktree")

	return cmd
}

func newOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <slug>",
		Short: "Open a worktree in the repository's IDE (see 'wt ide')",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := managerHere(cmd)
			if err != nil {
				return err
			}

			info, err := m.Find(cmd.Context(), args[0])
			if err != nil {
				return err
			}

			opened, err := m.OpenInIDE(cmd.Context(), info.Path)
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Opened %s in %s\n", info.Slug, opened.App)
			}

			return err
		},
		ValidArgsFunction: slugCompletion,
	}
}

func newGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Claude Code PreToolUse hook: block edits inside a main clone",
		Long: `Reads a PreToolUse hook payload on stdin and denies Edit, Write and NotebookEdit calls whose
target is inside a wt-managed main/ clone. Register it in ~/.claude/settings.json:

  "hooks": {"PreToolUse": [{
    "matcher": "Edit|Write|NotebookEdit",
    "hooks": [{"type": "command", "command": "wt guard"}]
  }]}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return guard.Run(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
}

// execClaude replaces the process with a Claude Code session running in path, so the terminal
// belongs to claude and not to wt.
func execClaude(path, repo, slug string, resume bool, extra []string) error {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return err
	}

	argv := session.Args(repo, slug, resume, extra)

	if err := os.Chdir(path); err != nil {
		return err
	}

	return syscall.Exec(bin, argv, os.Environ())
}

// containsCwd reports whether the working directory is dir or inside it.
func containsCwd(dir string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}

	// Compare real paths: $PWD and the argument may differ by symlinks (macOS /tmp, /var).
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}

	rel, err := filepath.Rel(abs, cwd)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func expandHome(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}

	return path
}
