// Package links shares gitignored files (local config, .env, IDE run configurations) between the
// main clone and its worktrees by symlinking them, so there is one copy to edit.
package links

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/martbock/wt/internal/config"
	"github.com/martbock/wt/internal/layout"
)

// Entry is one path in the links list of wt.yml.
type Entry struct {
	Pattern string
	Dir     bool
}

// String formats the entry the way it is written in wt.yml.
func (e Entry) String() string {
	if e.Dir {
		return filepath.ToSlash(e.Pattern) + "/"
	}

	return filepath.ToSlash(e.Pattern)
}

// NotListedError means paths that were to be removed from the links list are not in it.
type NotListedError struct{ Patterns []string }

func (e *NotListedError) Error() string {
	return fmt.Sprintf("not in %s: %s (see 'wt link ls')", layout.ConfigFile, strings.Join(e.Patterns, ", "))
}

// Status is what happened to one path.
type Status string

const (
	Linked  Status = "linked"
	Present Status = "present"
	Missing Status = "missing"
	Skipped Status = "skipped"
	Copied  Status = "copied"

	// Unlinked means a symlink into main/ was removed from a worktree.
	Unlinked Status = "unlinked"
)

// Action reports what was done for one path.
type Action struct {
	Path   string
	Status Status
	Detail string
}

// ParseEntry reads one path as written in wt.yml or on the command line.
func ParseEntry(line string) (Entry, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Entry{}, errors.New("empty path")
	}

	dir := strings.HasSuffix(line, "/")
	pattern := filepath.Clean(line)

	switch {
	case pattern == ".":
		return Entry{}, fmt.Errorf("%q would link all of main/", line)
	case filepath.IsAbs(pattern), pattern == "..", strings.HasPrefix(pattern, ".."+string(filepath.Separator)):
		return Entry{}, fmt.Errorf("%q must be relative to main/", line)
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return Entry{}, fmt.Errorf("%q: %w", line, err)
	}

	return Entry{Pattern: pattern, Dir: dir}, nil
}

// ParseEntries reads several paths, as given on the command line.
func ParseEntries(lines []string) ([]Entry, error) {
	entries := make([]Entry, 0, len(lines))
	for _, line := range lines {
		e, err := ParseEntry(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}

	return entries, nil
}

// Load reads the links list of wt.yml in the container. A missing file or list means nothing is
// linked.
func Load(container string) ([]Entry, error) {
	cfg, err := config.Load(container)
	if err != nil {
		return nil, err
	}

	entries, err := ParseEntries(cfg.Links)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", layout.ConfigFile, err)
	}

	return entries, nil
}

// AddToFile appends entries to the links list of wt.yml, creating the file from its template if
// there is none. Entries whose pattern is already listed are left out of the result and not
// written twice. Comments and the order of the existing items are kept.
func AddToFile(container string, entries []Entry) ([]Entry, error) {
	var added []Entry

	err := config.EditLinks(container, func(current []string) ([]string, error) {
		listed, err := ParseEntries(current)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", layout.ConfigFile, err)
		}

		have := make(map[string]bool, len(listed))
		for _, e := range listed {
			have[e.Pattern] = true
		}

		for _, e := range entries {
			if have[e.Pattern] {
				continue
			}
			have[e.Pattern] = true
			added = append(added, e)
			current = append(current, e.String())
		}

		return current, nil
	})

	return added, err
}

// RemoveFromFile deletes the items of the links list whose pattern equals one of entries' (a
// trailing slash does not matter) and returns the entries as they were listed. It changes nothing
// and returns a *NotListedError if any of them is not listed.
func RemoveFromFile(container string, entries []Entry) ([]Entry, error) {
	var removed []Entry

	err := config.EditLinks(container, func(current []string) ([]string, error) {
		listed, err := ParseEntries(current)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", layout.ConfigFile, err)
		}

		want := make(map[string]bool, len(entries))
		for _, e := range entries {
			want[e.Pattern] = true
		}

		var kept []string
		for i, e := range listed {
			if want[e.Pattern] {
				removed = append(removed, e)
				delete(want, e.Pattern)
				continue
			}
			kept = append(kept, current[i])
		}

		if len(want) > 0 {
			var missing []string
			for _, e := range entries {
				if want[e.Pattern] {
					missing = append(missing, e.String())
				}
			}

			return nil, &NotListedError{Patterns: missing}
		}

		return kept, nil
	})
	if err != nil {
		return nil, err
	}

	return removed, nil
}

// Apply symlinks every path matched by entries in mainDir to the same relative path in
// worktreeDir. The links are relative, so they survive moving the whole container.
func Apply(mainDir, worktreeDir string, entries []Entry) ([]Action, error) {
	var actions []Action

	for _, e := range entries {
		if err := prepareDir(mainDir, e); err != nil {
			return actions, err
		}

		matches, err := Match(mainDir, e)
		if err != nil {
			return actions, err
		}
		if len(matches) == 0 {
			actions = append(actions, Action{Path: e.Pattern, Status: Missing, Detail: "not in main/"})
			continue
		}

		for _, rel := range matches {
			action, err := linkOne(mainDir, worktreeDir, rel)
			if err != nil {
				return actions, err
			}
			actions = append(actions, action)
		}
	}

	return actions, nil
}

// Unlink removes the symlinks into mainDir that Apply would create for entries from worktreeDir.
// Anything else at those paths, such as a tracked file or a link that points elsewhere, stays,
// and so does everything in mainDir. Directories that the removal leaves empty are removed too.
func Unlink(mainDir, worktreeDir string, entries []Entry) ([]Action, error) {
	var actions []Action

	for _, e := range entries {
		// Matching in the worktree, not in main/, finds links whose target was since deleted.
		matches, err := Match(worktreeDir, e)
		if err != nil {
			return actions, err
		}

		for _, rel := range matches {
			dst := filepath.Join(worktreeDir, rel)
			if !pointsTo(dst, filepath.Join(mainDir, rel)) {
				continue
			}

			if err := os.Remove(dst); err != nil {
				return actions, err
			}
			pruneEmptyParents(filepath.Dir(dst), worktreeDir)

			actions = append(actions, Action{Path: rel, Status: Unlinked})
		}
	}

	return actions, nil
}

// pruneEmptyParents removes dir and its parents up to, but excluding, stop, for as long as they
// are empty real directories.
func pruneEmptyParents(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop+string(filepath.Separator)) {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// Seed copies the paths matched by entries from an existing clone into mainDir, without
// overwriting anything. It is used once, to bring local config into a freshly cloned main/.
func Seed(fromDir, mainDir string, entries []Entry) ([]Action, error) {
	var actions []Action

	for _, e := range entries {
		matches, err := Match(fromDir, e)
		if err != nil {
			return actions, err
		}
		if len(matches) == 0 {
			actions = append(actions, Action{Path: e.Pattern, Status: Missing, Detail: "not in " + fromDir})

			if err := prepareDir(mainDir, e); err != nil {
				return actions, err
			}
			continue
		}

		for _, rel := range matches {
			dst := filepath.Join(mainDir, rel)
			if _, err := os.Lstat(dst); err == nil {
				actions = append(actions, Action{Path: rel, Status: Present, Detail: "already in main/"})
				continue
			}
			if err := copyTree(filepath.Join(fromDir, rel), dst); err != nil {
				return actions, err
			}
			actions = append(actions, Action{Path: rel, Status: Copied})
		}
	}

	return actions, nil
}

// prepareDir creates a literal directory entry so a worktree can link to it before the IDE has
// written anything into it.
func prepareDir(root string, e Entry) error {
	if !e.Dir || hasGlob(e.Pattern) {
		return nil
	}

	return os.MkdirAll(filepath.Join(root, e.Pattern), 0o755)
}

// Match returns the sorted paths, relative to root, that e selects. Dangling symlinks count.
func Match(root string, e Entry) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(root, e.Pattern))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", e.Pattern, err)
	}

	rels := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, err := filepath.Rel(root, m)
		if err != nil {
			return nil, err
		}
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	return rels, nil
}

func linkOne(mainDir, worktreeDir, rel string) (Action, error) {
	src := filepath.Join(mainDir, rel)
	dst := filepath.Join(worktreeDir, rel)

	if _, err := os.Lstat(dst); err == nil {
		if pointsTo(dst, src) {
			return Action{Path: rel, Status: Present}, nil
		}

		// Tracked files land here too: a worktree checkout already has them.
		return Action{Path: rel, Status: Skipped, Detail: "already exists in the worktree"}, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return Action{}, err
	}

	target, err := filepath.Rel(filepath.Dir(dst), src)
	if err != nil {
		return Action{}, err
	}
	if err := os.Symlink(target, dst); err != nil {
		return Action{}, err
	}

	return Action{Path: rel, Status: Linked}, nil
}

// pointsTo reports whether dst is a relative symlink to src.
func pointsTo(dst, src string) bool {
	cur, err := os.Readlink(dst)

	return err == nil && !filepath.IsAbs(cur) && filepath.Join(filepath.Dir(dst), cur) == src
}

func hasGlob(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}

	return out.Close()
}
