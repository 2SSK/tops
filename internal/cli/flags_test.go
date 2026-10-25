package cli

import (
	"bytes"
	"testing"
)

func TestGlobalFlags(t *testing.T) {
	type flags struct {
		JSON    bool
		NoColor bool
		Env     string
	}

	tests := []struct {
		name string
		args []string
		want flags
	}{
		{"no flags", []string{"version"}, flags{}},
		{"json before command", []string{"--json", "version"}, flags{JSON: true}},
		{"json after command", []string{"version", "--json"}, flags{JSON: true}},
		{"no-color", []string{"version", "--no-color"}, flags{NoColor: true}},
		{"env", []string{"-e", "lab", "version"}, flags{Env: "lab"}},
		{"env long", []string{"version", "--env", "lab"}, flags{Env: "lab"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts Options
			cmd := NewRootCommand(&opts)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetArgs(tt.args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := (flags{opts.JSON, opts.NoColor, opts.Env}); got != tt.want {
				t.Errorf("options = %+v, want %+v", opts, tt.want)
			}
		})
	}
}
