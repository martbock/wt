package manager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/martbock/wt/internal/gitx"
	"github.com/martbock/wt/internal/ide"
	"github.com/martbock/wt/internal/layout"
	"github.com/martbock/wt/internal/links"
)

// hookMarker identifies a pre-commit hook that wt installed, so it can be told apart from one
// the user wrote.
const hookMarker = "installed by wt"

const preCommitHook = `#!/bin/sh
# ` + hookMarker + `: the main clone is read-only, commit in a worktree instead.
if [ "$(git rev-parse --git-dir)" = "$(git rev-parse --git-common-dir)" ]; then
  echo "wt: refusing to commit in the main clone. Create a worktree with 'wt new'." >&2
  exit 1
fi
`

// HookStatus is the state of the pre-commit guard after Init.
type HookStatus string

const (
	HookInstalled HookStatus = "installed"
	HookCurrent   HookStatus = "already installed"
	HookForeign   HookStatus = "left alone, a different pre-commit hook exists"
)

// InitResult reports what Init set up.
type InitResult struct {
	LinksFile    string
	LinksCreated bool
	Links        []links.Entry
	IDE          ChosenIDE
	Hook         HookStatus
}

// Init prepares a main clone for worktrees: it writes an empty wt.links, names the main
// JetBrains project after the repository, and installs a pre-commit hook that refuses commits in main/.
// It is safe to run repeatedly.
func (m Manager) Init(ctx context.Context) (InitResult, error) {
	res := InitResult{LinksFile: filepath.Join(m.Layout.Container, layout.LinksFile)}

	if _, err := os.Stat(res.LinksFile); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(res.LinksFile, []byte(links.Default), 0o644); err != nil {
			return res, err
		}
		res.LinksCreated = true
	} else if err != nil {
		return res, err
	}

	if err := os.MkdirAll(m.Layout.Worktrees, 0o755); err != nil {
		return res, err
	}

	chosen, err := m.IDE()
	if err != nil {
		return res, err
	}
	res.IDE = chosen

	// Without this the main clone's JetBrains window would be titled "main".
	if chosen.IDE.JetBrains {
		if err := ide.WriteName(m.Layout.Main, m.Layout.Repo); err != nil {
			return res, err
		}
		if err := m.ensureIgnored(ctx, m.Layout.Main, ide.NameFile); err != nil {
			return res, err
		}
	}

	hook, err := m.installHook(ctx)
	res.Hook = hook
	if err != nil {
		return res, err
	}

	res.Links, err = links.Load(m.Layout.Container)

	return res, err
}

func (m Manager) installHook(ctx context.Context) (HookStatus, error) {
	path, err := gitx.Run(ctx, m.Layout.Main, "rev-parse", "--path-format=absolute", "--git-path", "hooks/pre-commit")
	if err != nil {
		return "", err
	}

	existing, err := os.ReadFile(path)
	switch {
	case err == nil && !strings.Contains(string(existing), hookMarker):
		return HookForeign, nil
	case err == nil && string(existing) == preCommitHook:
		return HookCurrent, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(preCommitHook), 0o755); err != nil {
		return "", err
	}

	return HookInstalled, nil
}
