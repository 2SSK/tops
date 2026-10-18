package cli

import (
	"bytes"
	"testing"
)

func TestGlobalFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Options
	}{
		{"no flags", []string{"version"}, Options{File: "cluster.yaml"}},
		{"json before command", []string{"--json", "version"}, Options{JSON: true, File: "cluster.yaml"}},
		{"json after command", []string{"version", "--json"}, Options{JSON: true, File: "cluster.yaml"}},
		{"no-color", []string{"version", "--no-color"}, Options{NoColor: true, File: "cluster.yaml"}},
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
			if opts != tt.want {
				t.Errorf("options = %+v, want %+v", opts, tt.want)
			}
		})
	}
}
