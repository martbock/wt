// Package tui is the interactive mode of wt: a list of worktrees that can be entered, created
// and removed, and the shared-file list, from one screen.
package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/pflag"

	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/links"
	"github.com/martbock/wt/internal/manager"
	"github.com/martbock/wt/internal/session"
)

// Backend is what the interface needs from a managed repository. manager.Manager implements it.
type Backend interface {
	List(ctx context.Context) ([]manager.Info, error)
	PullRequests(ctx context.Context, infos []manager.Info) manager.PRs
	Create(ctx context.Context, opts manager.CreateOptions) (manager.Created, error)
	Remove(ctx context.Context, slug string, opts manager.RemoveOptions) (manager.Removed, error)
	Links() ([]manager.LinkInfo, error)
	AddLinks(ctx context.Context, paths []string) (manager.LinkChange, error)
	RemoveLinks(ctx context.Context, paths []string) (manager.LinkChange, error)
	SyncLinks(ctx context.Context) (manager.LinkChange, error)
}

// Options connects the interface to the outside world.
type Options struct {
	// Repo is the repository name, shown in the title.
	Repo string

	// Claude returns the command that runs a Claude session in a worktree.
	Claude func(info manager.Info, resume bool) (*exec.Cmd, error)

	// Open opens a worktree in the repository's IDE and returns that IDE's name.
	Open func(ctx context.Context, path string) (string, error)
}

// Run shows the interface until the user quits. Sessions started from it take over the terminal
// and hand it back when they end.
func Run(ctx context.Context, m manager.Manager) error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("wt ui needs an interactive terminal")
	}

	repo := m.Layout.Repo
	opts := Options{
		Repo: repo,
		Claude: func(info manager.Info, resume bool) (*exec.Cmd, error) {
			return session.Command(info.Path, repo, info.Slug, resume)
		},
		Open: func(ctx context.Context, path string) (string, error) {
			opened, err := m.OpenInIDE(ctx, path)
			return opened.App, err
		},
	}

	_, err := tea.NewProgram(newModel(ctx, m, opts), tea.WithAltScreen(), tea.WithContext(ctx)).Run()

	return err
}

type mode int

const (
	modeList mode = iota
	modeNew
	modeConfirmRemove
	modeLinks
	modeLinkAdd
	modeConfirmUnlink
)

type (
	loadedMsg struct {
		infos []manager.Info
		err   error
	}
	createdMsg struct {
		created manager.Created
		err     error
	}
	removedMsg struct {
		slug string
		res  manager.Removed
		err  error
	}
	prsMsg struct {
		prs manager.PRs
	}
	linksLoadedMsg struct {
		infos []manager.LinkInfo
		err   error
	}
	linkChangedMsg struct {
		summary string
		err     error
	}
	openedMsg struct {
		slug string
		app  string
		err  error
	}
	sessionEndedMsg struct {
		slug string
		err  error
	}
)

type model struct {
	// ctx outlives every command the model starts; bubbletea has no per-message context.
	ctx  context.Context //nolint:containedctx
	be   Backend
	opts Options

	mode   mode
	width  int
	height int

	infos  []manager.Info
	cursor int

	// loaded is false until the first listing arrives, so an empty screen is not mistaken for
	// "no worktrees".
	loaded bool

	// prs is what the last pull request lookup found. It is kept while a new lookup runs, so the
	// column does not blink on every refresh.
	prs       manager.PRs
	prLoading bool

	links       []manager.LinkInfo
	linksCursor int

	input   textinput.Model
	formErr string

	// pending is the worktree or linked path that a confirmation is about.
	pending     manager.Info
	pendingLink string

	// selectSlug moves the cursor to this worktree once the list has reloaded.
	selectSlug string

	// busy is shown while a slow operation runs; keys are ignored until it finishes.
	busy      string
	status    string
	statusErr bool
}

func newModel(ctx context.Context, be Backend, opts Options) model {
	in := textinput.New()
	in.Prompt = "> "
	in.CharLimit = 120
	in.Width = 48

	return model{ctx: ctx, be: be, opts: opts, input: in}
}

func (m model) Init() tea.Cmd {
	return m.load()
}

func (m model) load() tea.Cmd {
	return func() tea.Msg {
		infos, err := m.be.List(m.ctx)
		return loadedMsg{infos: infos, err: err}
	}
}

func (m model) loadPRs(infos []manager.Info) tea.Cmd {
	return func() tea.Msg {
		return prsMsg{prs: m.be.PullRequests(m.ctx, infos)}
	}
}

func (m model) loadLinks() tea.Cmd {
	return func() tea.Msg {
		infos, err := m.be.Links()
		return linksLoadedMsg{infos: infos, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case loadedMsg:
		m = m.onLoaded(msg)
		if msg.err != nil || len(m.infos) == 0 {
			return m, nil
		}

		// The network lookup runs after the list is on screen.
		m.prLoading = true

		return m, m.loadPRs(m.infos)

	case prsMsg:
		m.prs, m.prLoading = msg.prs, false
		return m, nil

	case createdMsg:
		m.busy = ""
		if msg.err != nil {
			m.fail(msg.err)
		} else {
			m.ok("Created %s on branch %s", msg.created.Slug, msg.created.Branch)
		}
		if msg.created.Path != "" {
			// A step after 'git worktree add' can fail and still leave the worktree.
			m.selectSlug = msg.created.Slug
		}

		return m, m.load()

	case removedMsg:
		m.busy = ""
		switch {
		case msg.err != nil:
			m.fail(msg.err)
		case msg.res.BranchDeleted:
			m.ok("Removed %s and deleted branch %s", msg.slug, msg.res.Branch)
		case msg.res.BranchKept != "":
			m.ok("Removed %s, kept branch %s: %s", msg.slug, msg.res.Branch, strings.TrimSpace(msg.res.BranchKept))
		default:
			m.ok("Removed %s", msg.slug)
		}

		return m, m.load()

	case linksLoadedMsg:
		if msg.err != nil {
			m.fail(msg.err)
			return m, nil
		}
		m.links = msg.infos
		m.linksCursor = clamp(m.linksCursor, len(m.links))

		return m, nil

	case linkChangedMsg:
		m.busy = ""
		if msg.err != nil {
			m.fail(msg.err)
		} else {
			m.ok("%s", msg.summary)
		}

		return m, m.loadLinks()

	case openedMsg:
		if msg.err != nil {
			m.fail(msg.err)
		} else {
			m.ok("Opened %s in %s", msg.slug, msg.app)
		}

		return m, nil

	case sessionEndedMsg:
		if msg.err != nil {
			m.fail(fmt.Errorf("claude session in %s: %w", msg.slug, msg.err))
		} else {
			m.ok("Claude session in %s ended", msg.slug)
		}

		return m, m.load()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy != "" {
			return m, nil
		}

		return m.onKey(msg)
	}

	// Cursor blinking and the like.
	if m.typing() {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m model) onLoaded(msg loadedMsg) model {
	m.loaded = true
	if msg.err != nil {
		m.fail(msg.err)
		return m
	}

	m.infos = msg.infos
	m.cursor = clamp(m.cursor, len(m.infos))

	if m.selectSlug != "" {
		for i, info := range m.infos {
			if info.Slug == m.selectSlug {
				m.cursor = i
			}
		}
		m.selectSlug = ""
	}

	return m
}

func (m model) typing() bool {
	return m.mode == modeNew || m.mode == modeLinkAdd
}

func (m model) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeNew:
		return m.onNewKey(msg)
	case modeLinkAdd:
		return m.onLinkAddKey(msg)
	case modeConfirmRemove:
		return m.onConfirmRemoveKey(msg)
	case modeConfirmUnlink:
		return m.onConfirmUnlinkKey(msg)
	case modeLinks:
		return m.onLinksKey(msg)
	default:
		return m.onListKey(msg)
	}
}

func (m model) onListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	info, hasSel := m.selected()

	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		m.cursor = clamp(m.cursor-1, len(m.infos))
	case "down", "j":
		m.cursor = clamp(m.cursor+1, len(m.infos))
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = clamp(len(m.infos)-1, len(m.infos))
	case "r":
		m.status = ""
		return m, m.load()
	case "n":
		return m.startInput(modeNew, "login-bug --prefix fix/   or   --branch feat/login")
	case "l":
		m.mode = modeLinks
		m.status = ""
		return m, m.loadLinks()
	case "enter":
		if hasSel {
			return m.startSession(info, false)
		}
	case "c":
		if hasSel {
			return m.startSession(info, true)
		}
	case "o":
		if hasSel && m.guardPresent(info) {
			return m, func() tea.Msg {
				app, err := m.opts.Open(m.ctx, info.Path)
				return openedMsg{slug: info.Slug, app: app, err: err}
			}
		}
	case "d", "x":
		if hasSel && m.guardPresent(info) {
			m.pending = info
			m.mode = modeConfirmRemove
			m.status = ""
		}
	}

	return m, nil
}

// guardPresent reports whether the worktree directory exists, and says so if it does not.
func (m *model) guardPresent(info manager.Info) bool {
	if info.Missing {
		m.fail(fmt.Errorf("%s: directory is missing (run 'git worktree prune')", info.Slug))
		return false
	}

	return true
}

func (m model) startSession(info manager.Info, resume bool) (tea.Model, tea.Cmd) {
	if !m.guardPresent(info) {
		return m, nil
	}

	cmd, err := m.opts.Claude(info, resume)
	if err != nil {
		m.fail(err)
		return m, nil
	}

	m.status = ""

	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return sessionEndedMsg{slug: info.Slug, err: err}
	})
}

func (m model) startInput(to mode, placeholder string) (tea.Model, tea.Cmd) {
	m.mode = to
	m.formErr = ""
	m.status = ""
	m.input.Reset()
	m.input.Placeholder = placeholder

	return m, m.input.Focus()
}

func (m model) onNewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.formErr = ""

		return m, nil
	case "enter":
		opts, err := parseNewInput(strings.Fields(m.input.Value()))
		if err != nil {
			// Stay in the prompt so a typo does not cost the whole input.
			m.formErr = err.Error()
			return m, nil
		}

		m.mode = modeList
		m.formErr = ""
		m.busy = "Creating " + cmp.Or(opts.Slug, opts.Branch) + " (fetching origin first)…"

		return m, func() tea.Msg {
			created, err := m.be.Create(m.ctx, opts)
			return createdMsg{created: created, err: err}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.formErr = ""

	return m, cmd
}

// parseNewInput reads the new-worktree prompt, which takes the arguments of 'wt new': an optional
// slug, --prefix and --branch.
func parseNewInput(fields []string) (manager.CreateOptions, error) {
	var opts manager.CreateOptions

	flags := pflag.NewFlagSet("new", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.Prefix, "prefix", "", "")
	flags.StringVar(&opts.Branch, "branch", "", "")

	if err := flags.Parse(fields); err != nil {
		return opts, err
	}

	if flags.NArg() > 1 {
		return opts, fmt.Errorf("expected one slug, got %q", flags.Args())
	}
	if flags.NArg() == 1 {
		opts.Slug = flags.Arg(0)
	}

	if opts.Slug == "" && opts.Branch == "" {
		return opts, errors.New("enter a slug, or --branch <name>")
	}
	if opts.Branch != "" && opts.Prefix != "" {
		return opts, errors.New("--prefix and --branch cannot be combined")
	}
	if opts.Slug != "" {
		if err := layout.ValidateSlug(opts.Slug); err != nil {
			return opts, err
		}
	}
	if _, err := layout.NormalizePrefix(opts.Prefix); err != nil {
		return opts, err
	}

	return opts, nil
}

func (m model) onConfirmRemoveKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = modeList
	if msg.String() != "y" {
		m.status = "Canceled"
		return m, nil
	}

	info := m.pending
	m.busy = "Removing " + info.Slug + "…"

	return m, func() tea.Msg {
		// The prompt said that uncommitted changes are discarded, so force is what was agreed to.
		res, err := m.be.Remove(m.ctx, info.Slug, manager.RemoveOptions{Force: info.Dirty})
		return removedMsg{slug: info.Slug, res: res, err: err}
	}
}

func (m model) onLinksKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "l", "backspace":
		m.mode = modeList
		m.status = ""
	case "up", "k":
		m.linksCursor = clamp(m.linksCursor-1, len(m.links))
	case "down", "j":
		m.linksCursor = clamp(m.linksCursor+1, len(m.links))
	case "r":
		return m, m.loadLinks()
	case "a", "n":
		return m.startInput(modeLinkAdd, ".env  or  config/local*.yaml  or  .idea/runConfigurations/")
	case "d", "x":
		if len(m.links) > 0 {
			m.pendingLink = m.links[m.linksCursor].Entry.String()
			m.mode = modeConfirmUnlink
			m.status = ""
		}
	case "s":
		m.busy = "Linking everything in wt.links into every worktree…"

		return m, func() tea.Msg {
			c, err := m.be.SyncLinks(m.ctx)
			return linkChangedMsg{summary: summarize("Synced wt.links", c), err: err}
		}
	}

	return m, nil
}

func (m model) onLinkAddKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeLinks
		m.formErr = ""

		return m, nil
	case "enter":
		path := strings.TrimSpace(m.input.Value())
		if _, err := links.ParseEntry(path); err != nil {
			m.formErr = err.Error()
			return m, nil
		}

		m.mode = modeLinks
		m.formErr = ""
		m.busy = "Linking " + path + " into every worktree…"

		return m, func() tea.Msg {
			c, err := m.be.AddLinks(m.ctx, []string{path})
			return linkChangedMsg{summary: summarize("Added "+path, c), err: err}
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.formErr = ""

	return m, cmd
}

func (m model) onConfirmUnlinkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = modeLinks
	if msg.String() != "y" {
		m.status = "Canceled"
		return m, nil
	}

	path := m.pendingLink
	m.busy = "Unlinking " + path + " from every worktree…"

	return m, func() tea.Msg {
		c, err := m.be.RemoveLinks(m.ctx, []string{path})
		return linkChangedMsg{summary: summarize("Removed "+path, c), err: err}
	}
}

func (m model) selected() (manager.Info, bool) {
	if len(m.infos) == 0 {
		return manager.Info{}, false
	}

	return m.infos[m.cursor], true
}

func (m *model) ok(format string, args ...any) {
	m.status, m.statusErr = fmt.Sprintf(format, args...), false
}

func (m *model) fail(err error) {
	m.status, m.statusErr = err.Error(), true
}

// summarize says what a wt.links change did across the worktrees, in one line.
func summarize(what string, c manager.LinkChange) string {
	var linked, unlinked, skipped int
	var missing bool

	for _, w := range c.Worktrees {
		for _, a := range w.Actions {
			switch a.Status {
			case links.Linked:
				linked++
			case links.Unlinked:
				unlinked++
			case links.Skipped:
				skipped++
			case links.Missing:
				missing = true
			}
		}
	}

	var parts []string
	if linked > 0 {
		parts = append(parts, plural(linked, "link")+" created")
	}
	if unlinked > 0 {
		parts = append(parts, plural(unlinked, "link")+" removed")
	}
	if skipped > 0 {
		parts = append(parts, plural(skipped, "file")+" left alone because a worktree already has "+pronoun(skipped))
	}
	if missing {
		parts = append(parts, "nothing in main/ yet")
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing to change in "+plural(len(c.Worktrees), "worktree"))
	}

	return what + ": " + strings.Join(parts, ", ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}

func pronoun(n int) string {
	if n == 1 {
		return "it"
	}

	return "them"
}

// clamp keeps a cursor inside a list of n items.
func clamp(i, n int) int {
	return max(0, min(i, n-1))
}
