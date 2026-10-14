package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/martbock/wt/internal/manager"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "27", Dark: "75"})
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "28", Dark: "42"})
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "214"})
	mergedStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "92", Dark: "141"})
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "124", Dark: "203"})
)

// chrome is the number of lines around the list: title, blank, header, blank, status, prompt/help.
const chrome = 7

func (m model) View() string {
	var b strings.Builder

	switch m.mode {
	case modeLinks, modeLinkAdd, modeConfirmUnlink:
		m.viewLinks(&b)
	default:
		m.viewList(&b)
	}

	b.WriteString("\n")
	b.WriteString(m.viewFooter())

	if m.width > 0 {
		return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String())
	}

	return b.String()
}

func (m model) viewList(b *strings.Builder) {
	fmt.Fprintf(b, "%s  %s%s\n\n", titleStyle.Render("wt · "+m.opts.Repo), dimStyle.Render(plural(len(m.infos), "worktree")), m.prNote())

	if !m.loaded {
		b.WriteString(dimStyle.Render("  Loading…") + "\n")
		return
	}
	if len(m.infos) == 0 {
		b.WriteString(dimStyle.Render("  No worktrees. Press n to create one.") + "\n")
		return
	}

	rows := make([]listRow, len(m.infos))
	slugW, branchW, prW, mergedW := len("SLUG"), len("BRANCH"), len("PR"), len("MERGED")
	for i, info := range m.infos {
		rows[i] = m.rowOf(info)

		slugW = max(slugW, len(info.Slug))
		branchW = max(branchW, len(branchOf(info)))
		prW = max(prW, len(rows[i].pr))
		mergedW = max(mergedW, len(rows[i].merged))
	}

	head := fmt.Sprintf("%-*s  %-*s  %5s  %-16s  %-*s  %s", slugW, "SLUG", branchW, "BRANCH", "AHEAD", "STATE", prW, "PR", "MERGED")
	fmt.Fprintf(b, "  %s\n", dimStyle.Render(head))

	start, end := window(m.cursor, len(m.infos), m.rows())
	for i := start; i < end; i++ {
		info, row := m.infos[i], rows[i]

		line := fmt.Sprintf("%-*s  %-*s  %5d  ", slugW, info.Slug, branchW, branchOf(info), info.Ahead)
		cells := row.state.Render(fmt.Sprintf("%-16s", row.stateText)) + "  " +
			row.prStyle.Render(fmt.Sprintf("%-*s", prW, row.pr)) + "  " +
			row.mergedStyle.Render(row.merged)

		if i == m.cursor {
			b.WriteString(cursorStyle.Render("▸ ") + titleStyle.Render(line) + cells + "\n")
		} else {
			b.WriteString("  " + line + cells + "\n")
		}
	}

	if start > 0 || end < len(m.infos) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  %d–%d of %d", start+1, end, len(m.infos))) + "\n")
	}
}

// listRow holds the text and style of the status cells of one worktree.
type listRow struct {
	stateText, pr, merged       string
	state, prStyle, mergedStyle lipgloss.Style
}

func (m model) rowOf(info manager.Info) listRow {
	r := listRow{stateText: "clean", state: okStyle}

	switch {
	case info.Missing:
		r.stateText, r.state = "dir missing", errStyle
	case info.Dirty:
		r.stateText, r.state = "dirty", warnStyle
	}

	pr := m.prs.For(info.Slug)

	r.pr, r.prStyle = m.prs.Label(info.Slug), dimStyle
	switch {
	case m.prLoading && !m.prs.Looked:
		r.pr = "…"
	case pr == nil:
	case pr.State == manager.PRMerged:
		r.prStyle = mergedStyle
	case pr.State == manager.PRClosed:
		r.prStyle = errStyle
	case pr.Draft:
	case pr.Review == "CHANGES_REQUESTED":
		r.prStyle = warnStyle
	default:
		r.prStyle = okStyle
	}

	landed := info.Landed(pr)
	r.merged, r.mergedStyle = landed.Label(), lipgloss.NewStyle()
	switch landed {
	case manager.MergeMerged:
		r.mergedStyle = mergedStyle
	case manager.MergeRemoteGone:
		r.mergedStyle = warnStyle
	case manager.MergeEmpty, manager.MergeUnknown:
		r.mergedStyle = dimStyle
	case manager.MergeNone:
	}

	return r
}

// prNote tells, next to the title, that pull requests are being looked up or could not be.
func (m model) prNote() string {
	switch {
	case m.prLoading:
		return dimStyle.Render("  · checking pull requests…")
	case m.prs.Err != nil:
		return warnStyle.Render("  · pull requests unavailable: " + m.prs.Err.Error())
	}

	return ""
}

func (m model) viewLinks(b *strings.Builder) {
	fmt.Fprintf(b, "%s  %s\n\n", titleStyle.Render("wt · "+m.opts.Repo+" · shared files"), dimStyle.Render("wt.links"))

	if len(m.links) == 0 {
		b.WriteString(dimStyle.Render("  Nothing is shared between worktrees. Press a to add a path.") + "\n")
		return
	}

	pathW := len("PATH")
	for _, info := range m.links {
		pathW = max(pathW, len(info.Entry.String()))
	}

	fmt.Fprintf(b, "  %s\n", dimStyle.Render(fmt.Sprintf("%-*s  %s", pathW, "PATH", "IN MAIN")))

	start, end := window(m.linksCursor, len(m.links), m.rows())
	for i := start; i < end; i++ {
		info := m.links[i]

		inMain := dimStyle.Render("nothing yet")
		if n := len(info.Matches); n > 0 {
			inMain = strconv.Itoa(n)
		}

		line := fmt.Sprintf("%-*s  ", pathW, info.Entry)
		if i == m.linksCursor {
			b.WriteString(cursorStyle.Render("▸ ") + titleStyle.Render(line) + inMain + "\n")
		} else {
			b.WriteString("  " + line + inMain + "\n")
		}
	}
}

// viewFooter is the status line, then whatever the current mode asks for: a prompt, a question
// or the key help.
func (m model) viewFooter() string {
	var b strings.Builder

	switch {
	case m.busy != "":
		b.WriteString(warnStyle.Render(m.busy))
		return b.String()
	case m.status != "" && m.statusErr:
		b.WriteString(errStyle.Render(m.status) + "\n")
	case m.status != "":
		b.WriteString(okStyle.Render(m.status) + "\n")
	default:
		b.WriteString("\n")
	}

	switch m.mode {
	case modeNew:
		b.WriteString("New worktree  " + dimStyle.Render("[slug] [--prefix fix/] [--branch name]") + "\n" + m.input.View())
		b.WriteString(m.formErrLine())
		b.WriteString("\n" + dimStyle.Render("enter create · esc cancel"))
	case modeLinkAdd:
		b.WriteString("Share with every worktree  " + dimStyle.Render("path relative to main/, globs and a trailing / allowed") + "\n" + m.input.View())
		b.WriteString(m.formErrLine())
		b.WriteString("\n" + dimStyle.Render("enter add · esc cancel"))
	case modeConfirmRemove:
		b.WriteString(m.removeQuestion())
	case modeConfirmUnlink:
		b.WriteString(warnStyle.Render(fmt.Sprintf("Stop sharing %s? Its links are removed from every worktree; the files in main/ stay.", m.pendingLink)) +
			"  " + dimStyle.Render("y confirm · any other key cancels"))
	case modeLinks:
		b.WriteString(dimStyle.Render("↑/↓ move · a add · d remove · s sync into every worktree · r refresh · esc back"))
	default:
		b.WriteString(dimStyle.Render("↑/↓ move · enter session · c continue · o open · n new · d remove · l files · r refresh · q quit"))
	}

	return b.String()
}

func (m model) formErrLine() string {
	if m.formErr == "" {
		return ""
	}

	return "\n" + errStyle.Render(m.formErr)
}

func (m model) removeQuestion() string {
	info := m.pending

	q := fmt.Sprintf("Remove %s? Its branch %s is deleted only if no commits are lost.", info.Slug, info.Branch)
	if info.Branch == "" {
		q = fmt.Sprintf("Remove %s?", info.Slug)
	}

	if info.Dirty {
		return errStyle.Render(fmt.Sprintf("%s has uncommitted or untracked changes. Remove it and DISCARD them?", info.Slug)) +
			"  " + dimStyle.Render("y confirm · any other key cancels")
	}

	return warnStyle.Render(q) + "  " + dimStyle.Render("y confirm · any other key cancels")
}

// rows is how many list rows fit on screen. Without a known height, all of them.
func (m model) rows() int {
	if m.height <= 0 {
		return 1 << 20
	}

	return max(3, m.height-chrome-3)
}

// window picks the slice of a list of total items, rows at most, that keeps the cursor visible.
func window(cursor, total, rows int) (start, end int) {
	if total <= rows {
		return 0, total
	}

	start = max(0, min(cursor-rows/2, total-rows))

	return start, start + rows
}

func branchOf(info manager.Info) string {
	if info.Branch == "" {
		return "(detached)"
	}

	return info.Branch
}
