package ide_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/martbock/wt/internal/ide"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		in        string
		app       string
		jetBrains bool
		known     bool
	}{
		{"goland", "GoLand", true, true},
		{"GoLand", "GoLand", true, true},
		{"vscode", "Visual Studio Code", false, true},
		{"Visual Studio Code", "Visual Studio Code", false, true},
		{"  cursor ", "Cursor", false, true},
		{"webstorm", "WebStorm", true, true},
		{"Sublime Text", "Sublime Text", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := ide.Resolve(tt.in)

			assert.Equal(t, tt.app, got.App)
			assert.Equal(t, tt.jetBrains, got.JetBrains)
			assert.Equal(t, tt.known, got.Known())
		})
	}
}

func TestDefault(t *testing.T) {
	t.Setenv(ide.AppEnv, "")
	assert.Equal(t, "GoLand", ide.Default().App)

	t.Setenv(ide.AppEnv, "vscode")
	assert.Equal(t, "Visual Studio Code", ide.Default().App)

	t.Setenv(ide.AppEnv, "Some App")
	assert.Equal(t, "Some App", ide.Default().App)
}
