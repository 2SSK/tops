package cli

import (
	"bytes"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

func TestUsageErrorsExitFour(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"unknown command", []string{"nope"}},
		{"unknown flag", []string{"--bogus"}},
		{"unknown subcommand flag", []string{"version", "--bogus"}},
		{"unknown argument", []string{"version", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if got := term.ExitCode(err); got != 4 {
				t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
			}
		})
	}
}
