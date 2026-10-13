package term

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestPrintError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "pain error",
			err:  errors.New("dial tcp 10.78.0.11:22: connection refused"),
			want: "✗ dial tcp 10.78.0.11:22: connection refused\n",
		},
		{
			name: "user error with why and fix",
			err: &UserError{
				Code: "secrets.mode",
				What: "secrets.env is readable by others",
				Why:  "mode 0644, must be 0600",
				Fix:  "chmod 600 secrets.env",
			},
			want: "✗ secrets.env is readable by others\n" +
				"  why: mode 0644, must be 0600\n" +
				"  fix: chmod 600 secrets.env\n" +
				"  (code: secrets.mode)\n",
		},
		{
			name: "wrapped user error without fix",
			err: fmt.Errorf("load spec: %w", &UserError{
				Code: "spec.invalid",
				What: "cluster.yaml has 2 errors"}),
			want: "✗ cluster.yaml has 2 errors\n" +
				"  (code: spec.invalid)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := PrintError(&out, true, tt.err); err != nil {
				t.Fatalf("PrintError: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("PrintError() = \n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
