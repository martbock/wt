// Package guard implements a Claude Code PreToolUse hook that keeps the main clone read-only.
package guard

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/martbock/wt/internal/layout"
)

// Input is the part of the PreToolUse hook payload the guard reads.
type Input struct {
	ToolName  string `json:"tool_name"`
	Cwd       string `json:"cwd"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
}

// Decision is the guard's verdict for one tool call.
type Decision struct {
	Deny   bool
	Reason string
}

type output struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// Decide denies a file write whose target lies inside a wt-managed main clone. Paths are
// compared lexically and symlinks are not followed: a symlinked config file edited through a
// worktree path is a worktree edit, not a main edit.
func Decide(in Input) Decision {
	target := in.ToolInput.FilePath
	if target == "" {
		target = in.ToolInput.NotebookPath
	}
	if target == "" {
		return Decision{}
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(in.Cwd, target)
	}

	main, ok := managedMain(filepath.Clean(target))
	if !ok {
		return Decision{}
	}

	return Decision{
		Deny: true,
		Reason: main + " is the read-only main clone. Create a worktree with 'wt new' " +
			"and work there instead.",
	}
}

// Run reads a hook payload from r and, when the call must be blocked, writes the deny
// response to w.
func Run(r io.Reader, w io.Writer) error {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return err
	}

	d := Decide(in)
	if !d.Deny {
		return nil
	}

	var out output
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = d.Reason

	return json.NewEncoder(w).Encode(out)
}

// managedMain finds the enclosing <container>/main directory of path, if the container holds a
// wt.yml file.
func managedMain(path string) (string, bool) {
	for dir := path; ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == layout.MainDir {
			if _, err := os.Stat(filepath.Join(filepath.Dir(dir), layout.ConfigFile)); err == nil {
				return dir, true
			}
		}

		if filepath.Dir(dir) == dir {
			return "", false
		}
	}
}
