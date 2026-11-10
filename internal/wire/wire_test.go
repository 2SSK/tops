package wire

import (
	"errors"
	"testing"
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
