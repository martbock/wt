package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Pull request states, as GitHub reports them.
const (
	PROpen   = "OPEN"
	PRMerged = "MERGED"
	PRClosed = "CLOSED"
)

// ErrNoGH means the GitHub CLI is not installed, so pull requests cannot be looked up.
var ErrNoGH = errors.New("gh (the GitHub CLI) is not installed")

const (
	prLookupTimeout = 20 * time.Second
	prConcurrency   = 4
)

// PullRequest is the pull request of a worktree's branch.
type PullRequest struct {
	Number int
	State  string // PROpen, PRMerged or PRClosed
	Draft  bool
	URL    string

	// HeadSHA is the branch tip the pull request was last seen at.
	HeadSHA string

	// Review is GitHub's review decision: APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED or empty.
	Review string
}

// Label is the short text for a pull request: "#12 open", "#12 draft", "#12 merged".
func (p PullRequest) Label() string {
	state := strings.ToLower(p.State)

	switch {
	case p.State == PROpen && p.Draft:
		state = "draft"
	case p.State == PROpen && p.Review == "APPROVED":
		state = "approved"
	case p.State == PROpen && p.Review == "CHANGES_REQUESTED":
		state = "changes requested"
	}

	return fmt.Sprintf("#%d %s", p.Number, state)
}

// PRs is what is known about the pull requests of a list of worktrees.
type PRs struct {
	// Looked is true once the lookup ran. Without it nothing is known.
	Looked bool

	// Err is why the lookup failed, if it did. Pull requests are then unknown, not absent.
	Err error

	// BySlug holds the pull request of each worktree that has one.
	BySlug map[string]PullRequest
}

// For returns the pull request of a worktree, if it has one and the lookup worked.
func (p PRs) For(slug string) *PullRequest {
	pr, ok := p.BySlug[slug]
	if !ok {
		return nil
	}

	return &pr
}

// Label is the pull request column for a worktree: its pull request, "none", "?" when the lookup
// failed, or "…" when it has not run yet.
func (p PRs) Label(slug string) string {
	switch {
	case p.Err != nil:
		return "?"
	case !p.Looked:
		return "…"
	}

	if pr := p.For(slug); pr != nil {
		return pr.Label()
	}

	return "none"
}

// PullRequests looks up the pull request of every worktree's branch with the GitHub CLI. It asks
// the network, so it takes a moment and is separate from List. One failed lookup fails them all:
// a partial answer would read as "no pull request".
func (m Manager) PullRequests(ctx context.Context, infos []Info) PRs {
	res := PRs{Looked: true, BySlug: map[string]PullRequest{}}

	bin, err := exec.LookPath("gh")
	if err != nil {
		res.Err = ErrNoGH
		return res
	}

	ctx, cancel := context.WithTimeout(ctx, prLookupTimeout)
	defer cancel()

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, prConcurrency)
	)

	for _, info := range infos {
		if info.Branch == "" || info.Missing {
			continue
		}

		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			pr, found, err := m.lookupPR(ctx, bin, info.Branch)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err != nil && res.Err == nil:
				res.Err = fmt.Errorf("%s: %w", info.Slug, err)
			case found:
				res.BySlug[info.Slug] = pr
			}
		}()
	}
	wg.Wait()

	if res.Err != nil {
		res.BySlug = nil
	}

	return res
}

func (m Manager) lookupPR(ctx context.Context, bin, branch string) (PullRequest, bool, error) {
	cmd := exec.CommandContext(ctx, bin, "pr", "list", "--head", branch, "--state", "all", "--limit", "10",
		"--json", "number,state,isDraft,url,headRefOid,reviewDecision")
	cmd.Dir = m.Layout.Main
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1")

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return PullRequest{}, false, errors.New(firstLine(msg))
		}

		return PullRequest{}, false, err
	}

	pr, found, err := parsePRs(out)

	return pr, found, err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

type ghPR struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	URL            string `json:"url"`
	HeadRefOid     string `json:"headRefOid"`
	ReviewDecision string `json:"reviewDecision"`
}

// parsePRs picks the pull request that describes a branch from gh's list: an open one if there
// is one, else the most recent. A branch name can be reused for several pull requests.
func parsePRs(data []byte) (PullRequest, bool, error) {
	var list []ghPR
	if err := json.Unmarshal(data, &list); err != nil {
		return PullRequest{}, false, fmt.Errorf("unexpected output from gh: %w", err)
	}

	var best *ghPR
	for i := range list {
		c := &list[i]

		switch {
		case best == nil:
			best = c
		case c.State == PROpen && best.State != PROpen:
			best = c
		case (c.State == PROpen) == (best.State == PROpen) && c.Number > best.Number:
			best = c
		}
	}
	if best == nil {
		return PullRequest{}, false, nil
	}

	return PullRequest{
		Number:  best.Number,
		State:   best.State,
		Draft:   best.IsDraft,
		URL:     best.URL,
		HeadSHA: best.HeadRefOid,
		Review:  best.ReviewDecision,
	}, true, nil
}
