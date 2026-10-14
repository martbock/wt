# wt

One read-only clone per repository, and a git worktree, branch and Claude Code session for every
task. Built for working on several tickets in parallel while reviewing each one in your IDE.

```
~/git/acme/api/                         the "container": a plain folder, not a git repo
├── main/                               the clone: stays on main, never edited
├── worktrees/
│   ├── fix-login-bug/                  branch PROJ-123-fix-login-bug
│   └── add-csv-export/                 branch feat/add-csv-export
└── wt.yml                              per-repository settings: which IDE to open, what to symlink
                                        from main/ into every worktree (nothing by default)
```

## Install

```bash
make install         # go install ./cmd/wt
make install-skill   # symlink skills/wt into ~/.claude/skills (the /wt skill)
```

Shell completion (slugs complete for `rm`, `path`, `claude`, `open`): `wt completion zsh --help`.

## Set up a repository

```bash
# New: clone into ./api (or a directory you name, like git clone), choosing the IDE and which
# gitignored files to share
wt clone git@github.com:acme/api.git \
    --ide goland \
    --link 'config/local*.yaml' --link .env --link '.env.*' --link .idea/runConfigurations/ \
    --seed ~/git/old/api      # copy those files from an existing clone

# A repository that needs nothing shared and opens in VS Code
wt clone git@github.com:acme/web.git ~/git/acme/web --ide vscode

# Existing <container>/main clone
cd <container>/main && wt init
```

Nothing is shared between worktrees unless you ask for it: `wt.yml` links nothing to start with, and
`--link` (repeatable) is how a repository opts in. `--seed` copies exactly the `--link` paths.

`wt init` is safe to repeat. It writes a `wt.yml` that links nothing if there is none, names the main clone's
JetBrains project after the repository (so its window is not called "main"), and installs a
`pre-commit` hook that refuses commits in `main/` (linked worktrees are unaffected). An existing,
different `pre-commit` hook is left alone and reported.

## Daily use

```bash
wt new login-bug --prefix PROJ-1234  # worktree "login-bug", branch PROJ-1234-login-bug, from origin/main
wt new login-bug --prefix fix/     # branch fix/login-bug (also chore/, feat/, ...)
wt new spike                       # no prefix needed: worktree "spike" on branch "spike"
wt new --branch feat/login         # work on an existing branch, e.g. to review a PR
wt claude fix-login-bug        # Claude session "api: fix-login-bug"
wt claude fix-login-bug -c     # continue that worktree's last session
wt open fix-login-bug          # open in this repository's IDE (see "IDE per repository")
wt ui                          # interactive mode, see below
wt ls                          # slug, branch, commits ahead, dirty?, merged?
wt ls --pr                     # ... and each branch's pull request (asks GitHub, slower)
wt rm fix-login-bug            # remove; also deletes the branch if no commits are lost
```

`wt new` takes a slug and accepts `--prefix`, `--branch`, `--base <ref>`, `--no-fetch` and
`--claude` (start the session right away).

- `--prefix` goes in front of the branch name, never the directory. It is used as typed (`fix/`,
  `chore/`, `PROJ-1234-`), except that a bare ticket ID such as `PROJ-1234` gets its dash. Nothing is
  ever guessed: without a prefix, the branch is named after the slug.
- `--branch <name>` works on that branch instead, which is what you want to review a PR in your
  IDE or with Claude. It is checked out if it exists locally, tracked from `origin` if it exists
  there (after a fetch), and created from `--base` otherwise. The slug is optional and derived
  from the branch, so `wt new --branch feat/PROJ-1234-login` makes the worktree `feat-proj-1234-login`.
  It cannot be combined with `--prefix`, and git refuses a branch that is already checked out in
  another worktree. Only branches on `origin` are found, not ones from forks.

`wt rm` refuses a worktree with uncommitted or untracked changes unless you pass `--force`. It
always removes the worktree, but deletes the branch only when that loses nothing: the branch has
no commits beyond `origin/main`, or its remote branch is gone (a merged PR whose branch was
deleted, which also covers squash merges). Otherwise it keeps the branch and says why.

## Pull request and merge status

`wt ls --pr` and `wt ui` show, for every worktree, its **PR** and whether its work is **MERGED**
into the default branch (plain `wt ls` leaves out the PR column):

```
SLUG            BRANCH                   AHEAD  STATE  PR           MERGED
fix-login-bug   PROJ-123-fix-login-bug   3      dirty  #812 open    no
add-csv-export  PROJ-120-add-csv-export  2      clean  #790 merged  yes
old-spike       old-spike                1      clean  none         remote gone
fresh           fresh                    0      clean  none         no commits
```

- **PR** comes from the GitHub CLI (`gh pr list --head <branch>`): `open`, `draft`, `approved`,
  `changes requested`, `merged` or `closed`; `none` when the branch has no pull request, `?` when
  the lookup failed (no `gh`, not logged in, offline). The lookup asks GitHub, so it is opt-in for
  `wt ls` (`--pr`); `wt ui` shows the list first and fills the column in.
- **MERGED** is `yes` when every commit is in the default branch, also after a rebase merge, or
  when the pull request is merged and the branch has no commits made after it. `no commits` is a
  branch with nothing of its own (fresh, or merged by fast-forward: git cannot tell). `remote gone`
  is a pushed branch whose remote branch was deleted, which is what a squash-merged PR looks like
  when `gh` is unavailable.
- Merge status is as fresh as the last fetch. `wt new` and `wt rm` fetch; so does `git fetch`.

## Interactive mode: `wt ui`

A full-screen list of the repository's worktrees: slug, branch, commits ahead, clean or dirty,
pull request and merge status (the pull requests load in the background).

| Key | Does |
| --- | --- |
| `↑` `↓` / `j` `k` | move |
| `enter` | start a Claude session in the worktree, named `<repo>: <slug>` |
| `c` | continue the worktree's last Claude session |
| `o` | open it in the repository's IDE |
| `n` | new worktree: type `[slug] [--prefix P] [--branch B]`, e.g. `login-bug --prefix fix/` or just `spike` |
| `d` | remove it, after a yes/no; a dirty worktree asks explicitly before discarding changes |
| `l` | shared files: `a` add, `d` remove, `s` sync into every worktree |
| `r` / `q` | refresh / quit |

A Claude session takes over the terminal and brings you back to the list when it ends. `wt ui`
needs a terminal; every action is also available as a plain command.

## IDE per repository

`wt open` and the `o` key use the IDE chosen for the repository, so Go repositories can open in
GoLand and Node ones in VS Code. It is stored in `wt.yml` next to `main/`:

```bash
wt ide                    # show the IDE and where the choice comes from
wt ide set vscode         # goland, webstorm, idea, pycharm, vscode, cursor ... or any macOS app name
wt ide set "Sublime Text" # not a preset: runs `open -a "Sublime Text" <worktree>`
wt ide unset              # back to $WT_IDE_APP, then the global IDE, then GoLand
```

Precedence: `wt.yml`, then `$WT_IDE_APP` (a preset name or an app name), then the global IDE
(below), then GoLand. Only
JetBrains IDEs get a project name (`.idea/.name`); a VS Code repository is left without `.idea/`.
Switching IDE does not touch worktrees that already exist.

## Global settings: `wt global`

Settings for every repository live in `~/.config/wt/config.yml` (`$XDG_CONFIG_HOME/wt/config.yml`
when that is set), for example `ide: vscode`. A repository's own `wt.yml` wins over them. `wt global` edits the file; editing it by hand works too:

```bash
wt global                  # the file and the current settings
wt global set ide vscode   # the IDE for repositories that do not choose one
wt global unset ide
```

`ide` is the only setting so far.

## Shared files: `links` in `wt.yml`

Gitignored files that worktrees need (local config, `.env`, GoLand run configurations) live once,
in `main/`, and each worktree symlinks to them with a relative link. Editing `config/local.yaml`
anywhere edits it everywhere. Which files are worth sharing depends on the repository, so
nothing is shared until you list it, at `wt clone --link` or later:

```bash
wt link ls                             # what is shared, and how many files each path selects in main/
wt link add 'config/local*.yaml' .env  # list it AND link it into every existing worktree
wt link rm .env                        # unlist it AND remove the links from every existing worktree
wt link sync                           # after editing wt.yml by hand: link everything into every worktree
```

The paths are the `links` list in `wt.yml` (`wt new` reads it too):

```yaml
ide: goland
links:
  - config/local*.yaml
  - .env
  - .env.*
  - .idea/runConfigurations/
```

`wt` keeps the comments and any other keys when it rewrites the file.

- Globs are allowed. Paths that don't exist in `main/` yet are listed anyway, and linked once
  they exist (`wt link sync`).
- A trailing `/` marks a directory. It is created in `main/` if missing, and the whole directory
  is linked, so run configurations saved from inside a worktree land in `main/` too.
- Files a worktree already has (tracked ones such as `.env.example`) are never replaced.
- `wt link rm` removes only symlinks that point into `main/`, never the files in `main/`, and
  leaves everything else in a worktree alone. A path must be listed to be removed.
- Linked paths that `.gitignore` doesn't cover are added to `.git/info/exclude`, so they cannot
  be committed by accident. Those lines stay after `wt link rm`; they are harmless.

GoLand only writes run configurations to `.idea/runConfigurations/*.xml` when they are marked
**Store as project file** in the run configuration dialog. Otherwise they stay in `workspace.xml`,
which is per project and not shared.

## Keeping `main/` read-only

- The `pre-commit` hook installed by `wt init` blocks commits in `main/`.
- `wt guard` is a Claude Code `PreToolUse` hook that denies Edit, Write and NotebookEdit calls
  targeting a `main/` clone. It only acts inside containers that have a `wt.yml`, which `wt clone` and `wt init` always create. Register it in
  `~/.claude/settings.json`:

  ```json
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "wt guard" }]
      }
    ]
  }
  ```

  Paths are compared lexically, so a symlinked config file edited through a worktree path counts
  as a worktree edit. Bash commands are not inspected.

## Things to know

- **Permission deny rules and symlinks.** A rule like `Read(./config/local.yaml)` is relative to
  the project root. A session in a worktree can still reach the real file by its absolute path in
  `main/`. Use `**/config/local*.yaml` style patterns for anything sensitive.
- **Claude memory is per directory.** Every worktree starts with its own, empty auto-memory.
- **Shared infrastructure.** Worktrees share one set of local config, hence one database and one
  set of emulators. Run integration tests in one worktree at a time.
- **JetBrains IDEs name projects after the directory.** For repositories that use one, `wt` writes
  `.idea/.name` as `<repo> › <slug>`. If your version ignores it, prefix the directory instead.
  VS Code and other editors show the directory name, i.e. the slug.

## Development

```bash
make test    # race-enabled tests, real temporary git repositories
make lint
```

Layout: `cmd/wt` (entry point), `internal/cli` (cobra and rendering only), `internal/tui`
(bubbletea interactive mode, tested against a fake backend), `internal/manager` (the operations,
returning plain data), `internal/layout`, `internal/links`, `internal/config`
(`wt.yml` and the global `config.yml`), `internal/ide`, `internal/session`, `internal/guard`, `internal/gitx`.
