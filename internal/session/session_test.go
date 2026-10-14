package session_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/martbock/wt/internal/session"
)

func TestArgs(t *testing.T) {
	assert.Equal(t, []string{"claude", "--name", "api: spike"}, session.Args("api", "spike", false, nil))
	assert.Equal(t, []string{"claude", "--continue", "--model", "x"}, session.Args("api", "spike", true, []string{"--model", "x"}))
}
