// Package session builds the Claude Code invocation for a worktree.
package session

import "os/exec"

// Args is the argv for a Claude session in a worktree: named "<repo>: <slug>" so it is easy to
// find in /resume, or, with resume, the worktree's most recent session. extra is passed on.
func Args(repo, slug string, resume bool, extra []string) []string {
	argv := []string{"claude"}
	if resume {
		argv = append(argv, "--continue")
	} else {
		argv = append(argv, "--name", repo+": "+slug)
	}

	return append(argv, extra...)
}

// Command is claude, ready to run in the worktree at path.
func Command(path, repo, slug string, resume bool) (*exec.Cmd, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bin, Args(repo, slug, resume, nil)[1:]...)
	cmd.Dir = path

	return cmd, nil
}
