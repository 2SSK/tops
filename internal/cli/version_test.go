package cli

import (
	"bytes"
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

func TestNewBuildInfo(t *testing.T) {
	stamped := &debug.BuildInfo{
		GoVersion: "go1.26.8",
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "5c8da89f00d4a1b2c3d4e5f60718293a4b5c6d7e"},
		},
	}

	tests := []struct {
		name string
		bi   *debug.BuildInfo
		ok   bool
		want string
	}{
		{"stamped", stamped, true, "tops dev (5c8da89, go1.26.8)"},
		{"no commit", &debug.BuildInfo{GoVersion: "go1.26.8"}, true, "tops dev (unknown, go1.26.8)"},
		{"no build info", nil, false, "tops dev (unknown, )"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newBuildInfo(tt.bi, tt.ok).String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.HasPrefix(out.String(), "tops dev (") {
		t.Errorf("output = %q", out.String())
	}
}

func TestVersionJSON(t *testing.T) {
	var out bytes.Buffer
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var doc versionDocument
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if doc.Kind != "Version" || doc.TopsVersion != "dev" || doc.Tops.Version != "dev" {
		t.Errorf("document = %+v", doc)
	}
}
