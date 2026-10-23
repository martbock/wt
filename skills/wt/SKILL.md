---
name: wt
description: Create, list, open or remove git worktrees, each with its own branch and Claude session, and manage which files they share and which IDE opens them, using the `wt` CLI. Use when the user wants to start work on a task in isolation ("start a worktree for PROJ-123", "new worktree for X", "check out this branch / PR to review it", "what worktrees do I have", "clean up the finished worktrees", "spin up a session for this"), or when about to edit a repo whose main clone is meant to stay read-only.
---

# wt: worktrees with their own Claude session

`wt` manages one read-only clone per repository (`<container>/main`) plus a worktree per task
(`<container>/worktrees/<slug>`). It works from the main clone or from any worktree. Run
`wt --help` if a command below is not enough.

## Rules

- **Never edit files in `main/`.** If a task needs code changes and the working directory is a
  `main/` clone, create a worktree first. A `wt guard` hook and a pre-commit hook enforce this.
- **Never invent a Jira ticket ID or branch prefix.** Both are optional: with just a slug,
  `wt new <slug>` creates worktree and branch named `<slug>`. Pass `--prefix` only when the user
  gave one (`fix/`, `chore/`, `PROJ-1234`), and do not ask for one they didn't give.
- **Do not use `--force` on `wt rm`** unless the user asked to discard that worktree's changes.

## Commands

| Goal | Command |
| --- | --- |
| Turn an existing clone (repo directly in the directory) into the wt layout, in place | `wt convert <directory>` (`--link`, `--ide` as for `wt clone`) |
| New worktree and branch | `wt new fix-login-bug` |
| ...with a ticket or type prefix on the branch | `wt new login-bug --prefix PROJ-1234` / `--prefix fix/` |
| Worktree for an existing branch (review a PR) | `wt new --branch feat/login` (slug is derived; or `wt new review-1234 --branch feat/login`) |
| List worktrees (state, commits ahead, merged?) | `wt ls` (`--pr` to add each PR, slower: asks GitHub) |
| Path of a worktree | `wt path <slug>` |
| Open it in the repo's IDE | `wt open <slug>` |
| Show or set the repo's IDE | `wt ide` / `wt ide set vscode` / `wt ide unset` |
| List shared (symlinked) files | `wt link ls` |
| Share a file with every worktree, existing ones included | `wt link add .env 'config/local*.yaml'` |
| Stop sharing it | `wt link rm .env` |
| Link what the `links` in `wt.yml` list into existing worktrees | `wt link sync` |
| Remove it (and its branch, if no commits are lost) | `wt rm <slug>` |
| Remove all finished worktrees (clean, merged PR) and their branches | `wt purge` (`-n` to preview) |

`--prefix` only changes the branch name (`fix/login-bug`), not the directory. `--branch` checks out
a local branch, tracks one from `origin` after a fetch, or creates it from the base if it exists
nowhere, so a mistyped branch name silently creates a new branch: read the `branch` line in the
output (`existing`, `tracking origin/...` or `new`). `--branch` and `--prefix` cannot be combined.

The slug is short kebab-case (`fix-login-bug`), taken from the task. It becomes the directory
name, the IDE project name suffix and the Claude session name.

`wt convert` moves the clone to `<directory>/main`, so a shell that was inside it must `cd main`.
It refuses a clone that has linked worktrees; do not remove them to get around that unless the user
says so.

## Which worktrees are finished

`wt ls` shows each worktree's PR and a MERGED column. `yes` means its work is in the default
branch (or its PR is merged), so `wt rm` is safe; `remote gone` usually means a squash-merged PR.
Only clean up worktrees the user asked about, and never with `--force` unless asked. `wt purge`
removes every clean worktree whose PR is merged; run `wt purge -n` first and show the user the
list unless they asked for the purge outright.

## Shared files and the IDE

- Nothing is shared between worktrees by default: `wt.yml` links nothing to start with. If the user wants local
  config or `.env` available in worktrees, use `wt link add`; it also fixes worktrees that already
  exist. Do not guess which files to share; ask or look at what is gitignored in `main/`.
- `wt link rm` only deletes symlinks into `main/`, never the files in `main/`.
- The IDE is per repository (`wt.yml`), so a Go repository can use GoLand and a Node one VS Code.
  `wt open <slug>` uses it.
- `wt ui` is an interactive full-screen mode for the user. It needs a terminal, so do not run it
  yourself.

## Starting the session

A running session cannot hand its terminal to another one, so do not try to launch `claude`
yourself. After `wt new`, tell the user to run, in a new terminal tab:

```
wt claude <slug>
```

To pick up an earlier session in that worktree: `wt claude <slug> --continue`.

## Reporting back

After `wt new`, give the user the worktree path, the branch (and whether it was new, existing or tracking `origin`) and the exact `wt claude <slug>`
command. After `wt link add` or `wt link rm`, say in how many worktrees links were created or removed. After `wt rm`, say whether the branch was deleted or kept, and why.
