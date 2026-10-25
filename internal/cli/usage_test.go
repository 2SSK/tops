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
		{"-f is gone", []string{"validate", "-f", "cluster.yaml"}},
		{"completion takes no environment", []string{"-e", "lab", "completion", "zsh"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCommand(&Options{})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if got := term.ExitCode(err); got != 4 {
				t.Errorf("exit code = %d, want 4 (err: %v)", got, err)
			}
		})
	}
}

// TestCompletionWithoutEnv checks that refuseEnv lets completion run without -e. Cobra
// binds the script's writer to os.Stdout when NewRootCommand creates the command, so
// SetOut cannot capture it: the test checks the error only (go test hidesthe output).
func TestCompletionWithoutEnv(t *testing.T) {
	cmd := NewRootCommand(&Options{})
	cmd.SetArgs([]string{"completion", "zsh"})

	if err := cmd.Execute(); err != nil {
		t.Errorf("completion zsh: %v", err)
	}
}
