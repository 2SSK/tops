// Package term renders tops output: errors, symbols, tables and JSON.
package term

import (
	"context"
	"errors"
	"strings"
)

// UserError is an error meant for the operator: what happened, why, and how to fix it.
// Code is stable and appears in --json output.
type UserError struct {
	Code string
	What string
	Why  string
	Fix  string
}

func (e *UserError) Error() string { return e.What }

// ExitCode maps an  error to the process exit code
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}

	var ue *UserError
	if !errors.As(err, &ue) {
		return 1
	}

	switch {
	case strings.HasPrefix(ue.Code, "refused."), ue.Code == "lock.held":
		return 3
	case strings.HasPrefix(ue.Code, "spec."),
		strings.HasPrefix(ue.Code, "secrets."),
		strings.HasPrefix(ue.Code, "usage."):
		return 4

	default:
		return 1
	}
}
