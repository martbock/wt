package tui

import (
	"cmp"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/links"
	"github.com/martbock/wt/internal/manager"
)

type fakeBackend struct {
	infos    []manager.Info
	links    []manager.LinkInfo
	created  []manager.CreateOptions
	removed  []string
	purged   [][]string
	purgeErr error
	forced   []bool
	added    []string
	unlinks  []string
	synced   int
	err      error

	prs       manager.PRs
	prLookups int
}

func (f *fakeBackend) List(context.Context) ([]manager.Info, error) { return f.infos, f.err }

func (f *fakeBackend) PullRequests(context.Context, []manager.Info) manager.PRs {
	f.prLookups++
	return f.prs
}

func (f *fakeBackend) Create(_ context.Context, o manager.CreateOptions) (manager.Created, error) {
	f.created = append(f.created, o)
	slug := o.Slug
	branch := o.Prefix + o.Slug
	if o.Branch != "" {
		branch = o.Branch
		slug = cmp.Or(slug, "derived")
	}
	if f.err != nil {
		return manager.Created{}, f.err
	}
	f.infos = append(f.infos, manager.Info{Slug: slug, Branch: branch, Path: "/wt/" + slug})

	return manager.Created{Slug: slug, Branch: branch, Path: "/wt/" + slug}, nil
}

func (f *fakeBackend) Remove(_ context.Context, slug string, o manager.RemoveOptions) (manager.Removed, error) {
	f.removed = append(f.removed, slug)
	f.forced = append(f.forced, o.Force)

	kept := infosWithout(f.infos, slug)
	f.infos = kept

	return manager.Removed{Branch: slug, BranchDeleted: true}, f.err
}

func (f *fakeBackend) Purge(_ context.Context, slugs []string) []manager.Purged {
	f.purged = append(f.purged, slugs)

	var out []manager.Purged
	for _, slug := range slugs {
		p := manager.Purged{Slug: slug, Removed: manager.Removed{Branch: slug, BranchDeleted: true}}
		if f.purgeErr != nil {
			p.Err = f.purgeErr
		} else {
			f.infos = infosWithout(f.infos, slug)
		}
		out = append(out, p)
	}

	return out
}

func infosWithout(infos []manager.Info, slug string) []manager.Info {
	var out []manager.Info
	for _, i := range infos {
		if i.Slug != slug {
			out = append(out, i)
		}
	}

	return out
}

func (f *fakeBackend) Links() ([]manager.LinkInfo, error) { return f.links, nil }

func (f *fakeBackend) AddLinks(_ context.Context, p []string) (manager.LinkChange, error) {
	f.added = append(f.added, p...)

	return manager.LinkChange{Worktrees: []manager.WorktreeLinks{
		{Slug: "a", Actions: []links.Action{{Path: p[0], Status: links.Linked}}},
		{Slug: "b", Actions: []links.Action{{Path: p[0], Status: links.Linked}}},
	}}, nil
}

func (f *fakeBackend) RemoveLinks(_ context.Context, p []string) (manager.LinkChange, error) {
	f.unlinks = append(f.unlinks, p...)

	return manager.LinkChange{}, nil
}

func (f *fakeBackend) SyncLinks(context.Context) (manager.LinkChange, error) {
	f.synced++
	return manager.LinkChange{}, nil
}

type harness struct {
	t    *testing.T
	m    tea.Model
	be   *fakeBackend
	ran  []string
	open []string
}

func newHarness(t *testing.T, infos ...manager.Info) *harness {
	t.Helper()

	be := &fakeBackend{infos: infos}
	h := &harness{t: t, be: be}
	h.m = newModel(t.Context(), be, Options{
		Repo: "repo",
		Claude: func(info manager.Info, resume bool) (*exec.Cmd, error) {
			if info.Slug == "nobinary" {
				return nil, errors.New("claude: not found")
			}
			h.ran = append(h.ran, info.Slug+map[bool]string{true: " --continue", false: ""}[resume])

			return exec.Command("true"), nil
		},
		Open: func(_ context.Context, path string) (string, error) {
			h.open = append(h.open, path)
			return "VS Code", nil
		},
	})
	h.m, _ = h.m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.feed(h.m.Init())

	return h
}

// feed runs cmd and delivers its message, as bubbletea would. Commands that wait on a timer or
// the terminal, such as cursor blinking and running a process, are not run.
func (h *harness) feed(cmd tea.Cmd) {
	h.t.Helper()

	if cmd == nil {
		return
	}

	msg := cmd()
	switch msg := msg.(type) {
	case loadedMsg, prsMsg, createdMsg, removedMsg, purgedMsg, linksLoadedMsg, linkChangedMsg, openedMsg, sessionEndedMsg:
		var next tea.Cmd
		h.m, next = h.m.Update(msg)
		h.feed(next)
	case tea.BatchMsg:
		for _, c := range msg {
			h.feed(c)
		}
	}
}

// press sends keys. A name like "enter" or "esc" is a special key, anything else is typed.
func (h *harness) press(keys ...string) {
	h.t.Helper()

	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}

		var cmd tea.Cmd
		h.m, cmd = h.m.Update(msg)
		if h.m.(model).typing() {
			continue // focus and blink commands
		}
		h.feed(cmd)
	}
}

func (h *harness) typeText(s string) {
	h.t.Helper()

	for _, r := range s {
		h.m, _ = h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (h *harness) view() string { return h.m.View() }

func (h *harness) model() model { return h.m.(model) }

var (
	alpha = manager.Info{Slug: "alpha", Branch: "alpha", Path: "/wt/alpha"}
	beta  = manager.Info{Slug: "beta", Branch: "PROJ-1-beta", Path: "/wt/beta", Dirty: true, Ahead: 2}
)

func TestList_ShowsWorktreesAndMovesCursor(t *testing.T) {
	h := newHarness(t, alpha, beta)

	out := h.view()
	assert.Contains(t, out, "wt · repo")
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "PROJ-1-beta")
	assert.Contains(t, out, "dirty")
	assert.Equal(t, 0, h.model().cursor)

	h.press("j")
	assert.Equal(t, 1, h.model().cursor)
	h.press("down", "down")
	assert.Equal(t, 1, h.model().cursor, "stops at the last item")
	h.press("k")
	assert.Equal(t, 0, h.model().cursor)
}

func TestList_SaysLoadingUntilFirstListing(t *testing.T) {
	m := newModel(t.Context(), &fakeBackend{}, Options{Repo: "repo"})

	assert.Contains(t, m.View(), "Loading")
	assert.NotContains(t, m.View(), "No worktrees")
}

func TestList_EmptyHintsAtNew(t *testing.T) {
	h := newHarness(t)

	assert.Contains(t, h.view(), "Press n to create one")
	h.press("enter", "d", "o", "c") // nothing selected: nothing happens
	assert.Equal(t, modeList, h.model().mode)
	assert.Empty(t, h.ran)
}

func TestEnter_StartsAndContinuesSession(t *testing.T) {
	h := newHarness(t, alpha, beta)

	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd, "enter hands the terminal to claude")
	assert.Equal(t, []string{"alpha"}, h.ran)

	h.press("j")
	_, cmd = h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	require.NotNil(t, cmd)
	assert.Equal(t, []string{"alpha", "beta --continue"}, h.ran)
}

func TestEnter_ShowsErrorWhenClaudeIsMissing(t *testing.T) {
	h := newHarness(t, manager.Info{Slug: "nobinary", Path: "/wt/nobinary"})

	m, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Nil(t, cmd)
	assert.True(t, m.(model).statusErr)
	assert.Contains(t, m.(model).status, "not found")
}

func TestSessionEnd_ReloadsAndReports(t *testing.T) {
	h := newHarness(t, alpha)

	var cmd tea.Cmd
	h.be.infos = []manager.Info{alpha, beta}
	h.m, cmd = h.m.Update(sessionEndedMsg{slug: "alpha"})
	h.feed(cmd)

	assert.Contains(t, h.view(), "Claude session in alpha ended")
	assert.Len(t, h.model().infos, 2, "state may have changed during the session")
}

func TestOpen_UsesRepositoryIDE(t *testing.T) {
	h := newHarness(t, alpha)

	h.press("o")

	assert.Equal(t, []string{"/wt/alpha"}, h.open)
	assert.Contains(t, h.view(), "Opened alpha in VS Code")
}

func TestOpen_RefusesMissingDirectory(t *testing.T) {
	h := newHarness(t, manager.Info{Slug: "gone", Path: "/wt/gone", Missing: true})

	h.press("o")

	assert.Empty(t, h.open)
	assert.Contains(t, h.view(), "directory is missing")
}

func TestNew_CreatesWorktreeWithOptionalPrefix(t *testing.T) {
	h := newHarness(t, alpha)

	h.press("n")
	require.Equal(t, modeNew, h.model().mode)
	h.typeText("login-bug --prefix fix/")
	h.press("enter")

	assert.Equal(t, []manager.CreateOptions{{Prefix: "fix/", Slug: "login-bug"}}, h.be.created)
	assert.Equal(t, modeList, h.model().mode)
	assert.Contains(t, h.view(), "Created login-bug on branch fix/login-bug")

	sel, ok := h.model().selected()
	require.True(t, ok)
	assert.Equal(t, "login-bug", sel.Slug, "the cursor lands on the new worktree")

	h.press("n")
	h.typeText("spike")
	h.press("enter")
	assert.Equal(t, manager.CreateOptions{Slug: "spike"}, h.be.created[1])
}

func TestNew_ChecksOutExistingBranch(t *testing.T) {
	h := newHarness(t, alpha)

	h.press("n")
	h.typeText("--branch feat/login")
	h.press("enter")

	assert.Equal(t, []manager.CreateOptions{{Branch: "feat/login"}}, h.be.created)
	assert.Equal(t, modeList, h.model().mode)
	assert.Contains(t, h.view(), "Created derived on branch feat/login")
}

func TestNew_KeepsPromptOpenOnBadInput(t *testing.T) {
	h := newHarness(t)

	for _, bad := range []string{"Bad Slug", "PROJ-1", "proj1 slug", "a b c", "--bogus x", "--prefix", "--prefix fix/ --branch x", "--prefix fix/", "x --prefix a..b"} {
		h.press("n")
		h.typeText(bad)
		h.press("enter")

		assert.Equal(t, modeNew, h.model().mode, bad)
		assert.NotEmpty(t, h.model().formErr, bad)
		assert.Empty(t, h.be.created, bad)

		h.press("esc")
		assert.Equal(t, modeList, h.model().mode)
	}
}

func TestNew_EscCancels(t *testing.T) {
	h := newHarness(t)

	h.press("n")
	h.typeText("whatever")
	h.press("esc")

	assert.Equal(t, modeList, h.model().mode)
	assert.Empty(t, h.be.created)
}

func TestRemove_AsksFirst(t *testing.T) {
	h := newHarness(t, alpha, beta)

	h.press("d")
	assert.Equal(t, modeConfirmRemove, h.model().mode)
	assert.Contains(t, h.view(), "Remove alpha?")

	h.press("n")
	assert.Equal(t, modeList, h.model().mode)
	assert.Empty(t, h.be.removed)
	assert.Contains(t, h.view(), "Canceled")

	h.press("d", "y")
	assert.Equal(t, []string{"alpha"}, h.be.removed)
	assert.Equal(t, []bool{false}, h.be.forced, "a clean worktree is not force-removed")
	assert.Len(t, h.model().infos, 1)
	assert.Contains(t, h.view(), "Removed alpha and deleted branch alpha")
}

func TestRemove_DirtyNeedsAnExplicitDiscard(t *testing.T) {
	h := newHarness(t, beta)

	h.press("d")
	assert.Contains(t, h.view(), "DISCARD")

	h.press("esc")
	assert.Empty(t, h.be.removed, "esc is not a yes")

	h.press("d", "y")
	assert.Equal(t, []string{"beta"}, h.be.removed)
	assert.Equal(t, []bool{true}, h.be.forced)
}

func TestRemove_ErrorIsShown(t *testing.T) {
	h := newHarness(t, alpha)
	h.be.err = errors.New("boom")

	var cmd tea.Cmd
	h.m, cmd = h.m.Update(removedMsg{slug: "alpha", err: h.be.err})
	h.feed(cmd)

	assert.True(t, h.model().statusErr)
	assert.Contains(t, h.view(), "boom")
}

func TestBusy_IgnoresKeysExceptQuit(t *testing.T) {
	h := newHarness(t, alpha, beta)
	m := h.model()
	m.busy = "Creating…"
	h.m = m

	h.press("j", "d", "n")
	assert.Equal(t, 0, h.model().cursor)
	assert.Equal(t, modeList, h.model().mode)
	assert.Contains(t, h.view(), "Creating…")

	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestQuit(t *testing.T) {
	h := newHarness(t, alpha)

	_, cmd := h.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func linkInfo(t *testing.T, path string, matches ...string) manager.LinkInfo {
	t.Helper()

	e, err := links.ParseEntry(path)
	require.NoError(t, err)

	return manager.LinkInfo{Entry: e, Matches: matches}
}

func TestLinks_ListAddRemoveSync(t *testing.T) {
	h := newHarness(t, alpha)
	h.be.links = []manager.LinkInfo{linkInfo(t, ".env", ".env"), linkInfo(t, "config/local*.yaml")}

	h.press("l")
	require.Equal(t, modeLinks, h.model().mode)
	out := h.view()
	assert.Contains(t, out, "shared files")
	assert.Contains(t, out, ".env")
	assert.Contains(t, out, "nothing yet", "an entry that selects nothing in main/ is flagged")

	h.press("a")
	require.Equal(t, modeLinkAdd, h.model().mode)
	h.typeText("docs/notes.md")
	h.press("enter")
	assert.Equal(t, []string{"docs/notes.md"}, h.be.added)
	assert.Equal(t, modeLinks, h.model().mode)
	assert.Contains(t, h.view(), "Added docs/notes.md: 2 links created")

	h.press("j", "d")
	assert.Equal(t, modeConfirmUnlink, h.model().mode)
	assert.Contains(t, h.view(), "config/local*.yaml")
	h.press("y")
	assert.Equal(t, []string{"config/local*.yaml"}, h.be.unlinks)

	h.press("s")
	assert.Equal(t, 1, h.be.synced)

	h.press("esc")
	assert.Equal(t, modeList, h.model().mode)
}

func TestLinks_RejectsBadPathInPrompt(t *testing.T) {
	h := newHarness(t, alpha)
	h.press("l", "a")
	h.typeText("../secrets")
	h.press("enter")

	assert.Equal(t, modeLinkAdd, h.model().mode)
	assert.NotEmpty(t, h.model().formErr)
	assert.Empty(t, h.be.added)
}

func TestLinks_UnlinkCanBeCanceled(t *testing.T) {
	h := newHarness(t, alpha)
	h.be.links = []manager.LinkInfo{linkInfo(t, ".env", ".env")}

	h.press("l", "d", "n")

	assert.Empty(t, h.be.unlinks)
	assert.Equal(t, modeLinks, h.model().mode)
}

func TestSummarize(t *testing.T) {
	two := manager.LinkChange{Worktrees: []manager.WorktreeLinks{
		{Slug: "a", Actions: []links.Action{{Status: links.Linked}, {Status: links.Skipped}}},
		{Slug: "b", Actions: []links.Action{{Status: links.Missing}}},
	}}

	assert.Equal(t, "X: 1 link created, 1 file left alone because a worktree already has it, nothing in main/ yet", summarize("X", two))
	assert.Equal(t, "X: nothing to change in 0 worktrees", summarize("X", manager.LinkChange{}))
	assert.Equal(t, "X: 1 link removed", summarize("X", manager.LinkChange{Worktrees: []manager.WorktreeLinks{
		{Actions: []links.Action{{Status: links.Unlinked}}},
	}}))
}

func TestView_ScrollsToKeepCursorVisible(t *testing.T) {
	var infos []manager.Info
	for i := range 40 {
		slug := "wt" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		infos = append(infos, manager.Info{Slug: slug, Branch: slug, Path: "/wt/" + slug})
	}
	h := newHarness(t, infos...)
	h.m, _ = h.m.Update(tea.WindowSizeMsg{Width: 100, Height: 15})

	h.press("G")
	out := h.view()

	assert.Contains(t, out, infos[39].Slug)
	assert.NotContains(t, out, infos[0].Slug+"  ", "the top scrolled away")
	assert.LessOrEqual(t, strings.Count(out, "\n")+1, 15)
}

func TestWindow(t *testing.T) {
	tests := []struct{ cursor, total, rows, start, end int }{
		{0, 3, 10, 0, 3},
		{0, 20, 5, 0, 5},
		{10, 20, 5, 8, 13},
		{19, 20, 5, 15, 20},
	}

	for _, tt := range tests {
		start, end := window(tt.cursor, tt.total, tt.rows)
		assert.Equal(t, []int{tt.start, tt.end}, []int{start, end}, "%+v", tt)
	}
}

func TestPullRequests_LoadAfterTheListAndShowInTheTable(t *testing.T) {
	be := &fakeBackend{
		infos: []manager.Info{{Slug: "alpha", Branch: "alpha", Merge: manager.MergeEmpty}, beta, {Slug: "gamma", Branch: "gamma", Path: "/wt/gamma", Ahead: 1, Head: "abc", Merge: manager.MergeNone}},
		prs: manager.PRs{Looked: true, BySlug: map[string]manager.PullRequest{
			"alpha": {Number: 12, State: manager.PROpen, Review: "APPROVED"},
			"gamma": {Number: 9, State: manager.PRMerged, HeadSHA: "abc"},
		}},
	}
	m := newModel(t.Context(), be, Options{Repo: "repo"})
	m.width, m.height = 140, 30

	// The list is shown before the lookup has answered.
	var cmd tea.Cmd
	var next tea.Model = m
	next, cmd = next.Update(m.load()())
	require.NotNil(t, cmd, "a lookup is started once the list is there")
	assert.Equal(t, 0, be.prLookups)
	out := next.View()
	assert.Contains(t, out, "checking pull requests")
	assert.Contains(t, out, "alpha")

	next, _ = next.Update(cmd())
	out = next.View()

	assert.Equal(t, 1, be.prLookups)
	assert.NotContains(t, out, "checking pull requests")
	assert.Contains(t, out, "#12 approved")
	assert.Contains(t, out, "#9 merged")
	assert.Contains(t, out, "none", "a branch without a pull request")
	for _, col := range []string{"PR", "MERGED"} {
		assert.Contains(t, out, col)
	}

	rows := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for _, slug := range []string{"alpha", "beta", "gamma"} {
			if strings.Contains(line, slug+" ") {
				rows[slug] = line
			}
		}
	}
	assert.Contains(t, rows["gamma"], "yes", "a merged pull request means merged, even after a squash")
	assert.Contains(t, rows["alpha"], "no commits")
}

func TestPullRequests_UnavailableIsSaidButTheListStillWorks(t *testing.T) {
	be := &fakeBackend{infos: []manager.Info{alpha}, prs: manager.PRs{Looked: true, Err: errors.New("gh is not installed")}}
	h := newHarness(t, alpha)
	h.be.prs = be.prs

	h.press("r")
	out := h.view()

	assert.Contains(t, out, "pull requests unavailable: gh is not installed")
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "?")
}

func TestPullRequests_NotLookedUpWithoutWorktrees(t *testing.T) {
	h := newHarness(t)

	assert.Zero(t, h.be.prLookups)
}

func TestWindowSize_ClearsTheScreen(t *testing.T) {
	h := newHarness(t, alpha)

	var cmd tea.Cmd
	h.m, cmd = h.m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})

	require.NotNil(t, cmd)
	assert.Equal(t, tea.ClearScreen(), cmd(), "rows exposed by a larger window must not keep old content")
	assert.Equal(t, 160, h.model().width)
	assert.Equal(t, 50, h.model().height)
}

// purgeable are worktrees where only "done" and "done2" are clean with a merged pull request.
func purgeable(t *testing.T) *harness {
	t.Helper()

	done := manager.Info{Slug: "done", Branch: "done", Path: "/wt/done", Head: "d1", Ahead: 2}
	done2 := manager.Info{Slug: "done2", Branch: "done2", Path: "/wt/done2", Head: "d2", Ahead: 1}
	dirtyDone := manager.Info{Slug: "dirty-done", Branch: "dirty-done", Path: "/wt/dirty-done", Head: "x1", Dirty: true}
	moved := manager.Info{Slug: "moved", Branch: "moved", Path: "/wt/moved", Head: "new", Ahead: 3}
	open := manager.Info{Slug: "open", Branch: "open", Path: "/wt/open", Head: "o1", Ahead: 1}

	h := newHarness(t, done, done2, dirtyDone, moved, open)
	h.be.prs = manager.PRs{Looked: true, BySlug: map[string]manager.PullRequest{
		"done":       {Number: 1, State: manager.PRMerged, HeadSHA: "d1"},
		"done2":      {Number: 2, State: manager.PRMerged, HeadSHA: "d2"},
		"dirty-done": {Number: 3, State: manager.PRMerged, HeadSHA: "x1"},
		"moved":      {Number: 4, State: manager.PRMerged, HeadSHA: "old"},
		"open":       {Number: 5, State: manager.PROpen, HeadSHA: "o1"},
	}}
	h.press("r")

	return h
}

func TestPurge_AsksFirstThenRemovesCleanMergedWorktrees(t *testing.T) {
	h := purgeable(t)

	h.press("p")
	out := h.view()
	assert.Contains(t, out, "Remove 2 clean worktrees with a merged pull request")
	assert.Contains(t, out, "done, done2")
	assert.NotContains(t, out, "dirty-done,")
	assert.Empty(t, h.be.purged, "nothing happens before the yes")

	h.press("y")

	assert.Equal(t, [][]string{{"done", "done2"}}, h.be.purged)
	assert.Contains(t, h.view(), "Purged 2 worktrees: done, done2")
	assert.Len(t, h.model().infos, 3)
}

func TestPurge_AnyOtherKeyCancels(t *testing.T) {
	h := purgeable(t)

	h.press("p", "n")

	assert.Empty(t, h.be.purged)
	assert.Contains(t, h.view(), "Canceled")
	assert.Equal(t, modeList, h.model().mode)
}

func TestPurge_NothingToDo(t *testing.T) {
	h := newHarness(t, alpha)
	h.be.prs = manager.PRs{Looked: true, BySlug: map[string]manager.PullRequest{}}
	h.press("r", "p")

	assert.Contains(t, h.view(), "Nothing to purge")
	assert.Equal(t, modeList, h.model().mode)
}

func TestPurge_NeedsPullRequests(t *testing.T) {
	h := newHarness(t, alpha)
	h.be.prs = manager.PRs{Looked: true, Err: errors.New("gh is not installed")}
	h.press("r", "p")

	assert.Contains(t, h.view(), "cannot purge without pull requests: gh is not installed")
	assert.Equal(t, modeList, h.model().mode)
	assert.Empty(t, h.be.purged)
}

func TestPurge_WaitsForPullRequestLookup(t *testing.T) {
	h := newHarness(t, alpha)
	m := h.model()
	m.prLoading = true
	h.m = m

	h.press("p")

	assert.Contains(t, h.view(), "still checking pull requests")
	assert.Equal(t, modeList, h.model().mode)
}

func TestPurge_FailureIsShown(t *testing.T) {
	h := purgeable(t)
	h.be.purgeErr = errors.New("boom")

	h.press("p", "y")

	assert.Contains(t, h.view(), "purged 0, failed: done: boom; done2: boom")
}
