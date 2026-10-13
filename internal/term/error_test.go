package term

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestUserErrorMessage(t *testing.T) {
	err := &UserError{Code: "secrets.mode", What: "secrets.env is readable by others"}
	if got, want := err.Error(), "secrets.env is readable by others"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"no error", nil, 0},
		{"plain error", errors.New("dial tcp: connection refused"), 1},
		{"user error, runtime", &UserError{Code: "ssh.sudo_password"}, 1},
		{"safety refusal", &UserError{Code: "refused.ring_incomplete"}, 3},
		{"lock held", &UserError{Code: "lock.held"}, 3},
		{"invalid spec", &UserError{Code: "spec.invalid"}, 4},
		{"bad secrets", &UserError{Code: "secrets.mode"}, 4},
		{"bad usage", &UserError{Code: "usage.invalid"}, 4},
		{"wrapped user error", fmt.Errorf("load spec: %w", &UserError{Code: "spec.invalid"}), 4},
		{"interrupted", fmt.Errorf("apply: %w", context.Canceled), 130},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Errorf("ExitCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
