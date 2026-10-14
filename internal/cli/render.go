package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/links"
	"github.com/martbock/wt/internal/manager"
)

func renderInit(w io.Writer, l layout.Layout, res manager.InitResult, seeded []links.Action) {
	fmt.Fprintf(w, "%s\n", l.Container)
	fmt.Fprintf(w, "  main       %s\n", l.Main)
	fmt.Fprintf(w, "  worktrees  %s\n", l.Worktrees)

	if res.ConfigFile != "" {
		state := "kept"
		if res.ConfigCreated {
			state = "created"
		}
		fmt.Fprintf(w, "  wt.yml     %s (%s)\n", res.ConfigFile, state)
	}
	if res.IDE.IDE.App != "" {
		fmt.Fprintf(w, "  ide        %s (%s)\n", res.IDE.IDE.App, res.IDE.Source)
	}
	renderEntries(w, res.Links)
	if res.ConfigCreated && len(res.Links) == 0 {
		fmt.Fprintln(w, "  No files are shared between worktrees yet. Add some with 'wt link add <path>'.")
	}
	if res.Hook != "" {
		fmt.Fprintf(w, "  pre-commit hook: %s\n", res.Hook)
	}

	renderActions(w, "seeded", seeded, links.Copied, links.Present, links.Missing)
}

func renderEntries(w io.Writer, entries []links.Entry) {
	if len(entries) == 0 {
		return
	}

	fmt.Fprintln(w, "  shared with every worktree")
	for _, e := range entries {
		fmt.Fprintf(w, "    %s\n", e)
	}
}

func renderLinks(w io.Writer, infos []manager.LinkInfo) {
	if len(infos) == 0 {
		fmt.Fprintln(w, "No files are shared between worktrees. Add some with 'wt link add <path>'.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tIN MAIN")

	for _, info := range infos {
		in := "-"
		if n := len(info.Matches); n > 0 {
			in = strconv.Itoa(n)
		}

		fmt.Fprintf(tw, "%s\t%s\n", info.Entry, in)
	}

	_ = tw.Flush()
}

// renderLinkChange reports a change to the links in wt.yml and what it did in each worktree. verb is empty for
// a plain sync, which changes no entries.
func renderLinkChange(w io.Writer, verb string, c manager.LinkChange) {
	for _, e := range c.Changed {
		fmt.Fprintf(w, "%s links: %s\n", verb, e)
	}
	if verb != "" {
		for _, e := range c.Unchanged {
			fmt.Fprintf(w, "Already in wt.yml links: %s\n", e)
		}
	}

	if len(c.Worktrees) == 0 && len(c.Changed)+len(c.Unchanged) > 0 {
		fmt.Fprintln(w, "No worktrees yet; new ones pick this up.")
		return
	}

	var absent []string
	for _, wl := range c.Worktrees {
		renderActions(w, wl.Slug, wl.Actions, links.Linked, links.Unlinked, links.Skipped)

		for _, a := range wl.Actions {
			if a.Status == links.Missing && !slices.Contains(absent, a.Path) {
				absent = append(absent, a.Path)
			}
		}
	}

	for _, p := range absent {
		fmt.Fprintf(w, "Nothing at %s in main/ yet; it is linked once it exists ('wt link sync').\n", p)
	}
}

func renderCreated(w io.Writer, c manager.Created) {
	fmt.Fprintf(w, "Created worktree %s\n", c.Slug)
	fmt.Fprintf(w, "  path    %s\n", c.Path)

	switch c.Source {
	case manager.SourceLocal:
		fmt.Fprintf(w, "  branch  %s (existing)\n", c.Branch)
	case manager.SourceRemote:
		fmt.Fprintf(w, "  branch  %s (tracking origin/%s)\n", c.Branch, c.Branch)
	default:
		fmt.Fprintf(w, "  branch  %s (new, from %s)\n", c.Branch, c.Base)
	}

	renderActions(w, "links", c.Links, links.Linked)

	fmt.Fprintf(w, "\nNext:\n  wt claude %s\n  wt open %s\n", c.Slug, c.Slug)
}

// renderActions prints the actions whose status is in show, under a heading. Everything else,
// such as tracked files that a checkout already has, is expected and left out.
func renderActions(w io.Writer, heading string, actions []links.Action, show ...links.Status) {
	var lines []string
	for _, a := range actions {
		if !slices.Contains(show, a.Status) {
			continue
		}

		line := fmt.Sprintf("    %-8s %s", a.Status, a.Path)
		if a.Detail != "" {
			line += "  (" + a.Detail + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}

	fmt.Fprintf(w, "  %s\n", heading)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

// renderList prints the worktrees. prs is nil when pull requests were not looked up, which also
// leaves out their column.
func renderList(w io.Writer, infos []manager.Info, prs *manager.PRs) {
	if len(infos) == 0 {
		fmt.Fprintln(w, "No worktrees. Create one with 'wt new <slug>'.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	header := "SLUG\tBRANCH\tAHEAD\tSTATE"
	if prs != nil {
		header += "\tPR"
	}
	fmt.Fprintln(tw, header+"\tMERGED")

	for _, info := range infos {
		state := "clean"
		switch {
		case info.Missing:
			state = "directory missing (run 'git worktree prune')"
		case info.Dirty:
			state = "dirty"
		}

		branch := info.Branch
		if branch == "" {
			branch = "(detached)"
		}

		var pr *manager.PullRequest
		row := fmt.Sprintf("%s\t%s\t%d\t%s", info.Slug, branch, info.Ahead, state)
		if prs != nil {
			pr = prs.For(info.Slug)
			row += "\t" + prs.Label(info.Slug)
		}

		fmt.Fprintln(tw, row+"\t"+info.Landed(pr).Label())
	}

	_ = tw.Flush()

	if prs != nil && prs.Err != nil {
		fmt.Fprintf(w, "\nPull requests unavailable: %v\n", prs.Err)
	}
}

func renderRemoved(w io.Writer, res manager.Removed) {
	fmt.Fprintf(w, "Removed %s\n", res.Path)

	switch {
	case res.BranchDeleted:
		fmt.Fprintf(w, "Deleted branch %s\n", res.Branch)
	case res.BranchKept != "":
		fmt.Fprintf(w, "Kept branch %s: %s\n", res.Branch, strings.TrimSpace(res.BranchKept))
	}
}
