package term

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestWriteErrorJSON(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "user error", err: fmt.Errorf("load: %w", &UserError{
			Code: "secrets.mode",
			What: "secrets.env is readable by others",
			Why:  "mode 0644, must be 0600",
			Fix:  "chmod 600 secrets.env && tops validate",
		}),
			want: `{"kind":"Error","topsVersion":"dev","error":{"code":"secrets.mode","what":"secrets.env is readable by others","why":"mode 0644, must be 0600","fix":"chmod 600 secrets.env && tops validate"}}` + "\n",
		},
		{
			name: "plain error",
			err:  errors.New("connection refused"),
			want: `{"kind":"Error","topsVersion":"dev","error":{"code":"unexpected","what":"connection refused"}}` + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := WriteErrorJSON(&out, "dev", tt.err); err != nil {
				t.Fatalf("WriteErrorJSON: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
