package guard_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/martbock/wt/internal/guard"
	"github.com/martbock/wt/internal/layout"
)

func input(cwd, file string) guard.Input {
	var in guard.Input
	in.ToolName = "Edit"
	in.Cwd = cwd
	in.ToolInput.FilePath = file

	return in
}

func TestDecide(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "acme", "repo")
	mainDir := filepath.Join(container, "main")
	worktree := filepath.Join(container, "worktrees", "task")
	require.NoError(t, os.MkdirAll(filepath.Join(mainDir, "pkg"), 0o755))
	require.NoError(t, os.MkdirAll(worktree, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(container, layout.LinksFile), nil, 0o644))

	unmanaged := filepath.Join(root, "other", "main")
	require.NoError(t, os.MkdirAll(unmanaged, 0o755))

	tests := []struct {
		name string
		in   guard.Input
		deny bool
	}{
		{"file in main", input(mainDir, filepath.Join(mainDir, "pkg", "a.go")), true},
		{"file in main that does not exist yet", input(mainDir, filepath.Join(mainDir, "pkg", "new", "b.go")), true},
		{"relative path resolved against cwd", input(mainDir, "pkg/a.go"), true},
		{"file in a worktree", input(worktree, filepath.Join(worktree, "a.go")), false},
		{"main-named dir outside a container", input(unmanaged, filepath.Join(unmanaged, "a.go")), false},
		{"no path in the tool input", input(mainDir, ""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.deny, guard.Decide(tt.in).Deny)
		})
	}
}

func TestRun_WritesDenyResponse(t *testing.T) {
	container := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(container, "main"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(container, layout.LinksFile), nil, 0o644))

	payload := `{"tool_name":"Write","cwd":"` + container + `/main","tool_input":{"file_path":"` + container + `/main/x.go"}}`

	var out bytes.Buffer
	require.NoError(t, guard.Run(strings.NewReader(payload), &out))

	var got map[string]map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, "PreToolUse", got["hookSpecificOutput"]["hookEventName"])
	assert.Equal(t, "deny", got["hookSpecificOutput"]["permissionDecision"])
	assert.Contains(t, got["hookSpecificOutput"]["permissionDecisionReason"], "wt new")
}

func TestRun_AllowsSilently(t *testing.T) {
	var out bytes.Buffer

	require.NoError(t, guard.Run(strings.NewReader(`{"tool_name":"Edit","cwd":"/tmp","tool_input":{"file_path":"/tmp/x.go"}}`), &out))

	assert.Empty(t, out.String())
}
