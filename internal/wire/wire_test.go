package wire

import (
	"errors"
	"testing"
	"time"
)

func TestErrorIsAnError(t *testing.T) {
	var err error = &Error{Code: "helper.failed", Message: "read meminfo"}
	var we *Error
	if !errors.As(err, &we) || we.Code != "helper.failed" {
		t.Fatalf("errors.As: %v", err)
	}
	if got := err.Error(); got != "helper.failed: read meminfo" {
		t.Errorf("Error() = %q", got)
	}
}

// TestTimeout: every op gets the TRD's 60 s, except stage, which downloads releases, and
// schema, which migrates the databases.
func TestTimeout(t *testing.T) {
	for op, want := range map[Op]time.Duration{
		OpHello:    time.Minute,
		OpFacts:    time.Minute,
		OpValidate: time.Minute,
		OpLock:     time.Minute,
		OpStage:    10 * time.Minute,
		OpSchema:   10 * time.Minute,
	} {
		if got := Timeout(op); got != want {
			t.Errorf("Timeout(%s) = %v, want %v", op, got, want)
		}
	}
}
