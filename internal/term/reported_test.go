package term

import (
	"errors"
	"testing"
)

func TestReportedKeepsExitCode(t *testing.T) {
	err := error(&Reported{Err: &UserError{Code: "spec.invalid", What: "cluster.yaml is invalid"}})

	if got := ExitCode(err); got != 4 {
		t.Errorf("ExitCode(%v) = %d, want 4", err, got)
	}
	if got := err.Error(); got != "cluster.yaml is invalid" {
		t.Errorf("Error() = %q", got)
	}
	var r *Reported
	if !errors.As(err, &r) {
		t.Error("errors.As does not find *Reported")
	}
}
