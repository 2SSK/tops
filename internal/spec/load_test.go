package spec

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

func TestParseLab(t *testing.T) {
	data, err := os.ReadFile("testdata/lab.yaml")
	if err != nil {
		t.Fatal(err)
	}

	c, err := Parse("testdata/lab.yaml", data)
	if err != nil {
		t.Fatalf("Parse: %#v", err)
	}

	if c.Cluster != "tops-e2e" || c.Shards != 512 || len(c.Nodes) != 3 {
		t.Errorf("got cluster=%q shards=%d nodes=%d", c.Cluster, c.Shards, len(c.Nodes))
	}
	if c.Persistence.Postgres == nil || c.Persistence.Postgres.Host != "10.80.0.20" {
		t.Errorf("postgres = %+v", c.Persistence.Postgres)
	}
	if c.Nodes[1].Address != "10.80.0.12" {
		t.Errorf("nodes[1].address = %q", c.Nodes[1].Address)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantWhy string
	}{
		{"unknown key", "cluster: a\nshard: 512\n", "line 2: field shard not found"},
		{"wrong type", "cluster: a\nshards: many\n", "line 2: cannot unmarshal !!str `many` into int"},
		{"profile is gone", "cluster: a\nprofile: 2c4g\n", "line 2: field profile not found"},
		{"ui is gone", "cluster: a\nui:\n version: 2.54.1\n", "line 2: field ui not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("cluster.yaml", []byte(tt.yaml))

			var ue *term.UserError
			if !errors.As(err, &ue) {
				t.Fatalf("err = %v, want a *term.UserError", err)
			}
			if ue.Code != "spec.invalid" {
				t.Errorf("code = %q, want spec.invalid", ue.Code)
			}
			if !strings.Contains(ue.Why, tt.wantWhy) {
				t.Errorf("why = %q, want it to contain %q", ue.Why, tt.wantWhy)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	c, err := Load("testdata/lab.yaml")
	if err != nil {
		t.Fatalf("Load: %#v", err)
	}
	if c.SSH.Port != 22 {
		t.Errorf("ssh.port = %d, want the  default 22 (Load must apply Defaults)", c.SSH.Port)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("testdata/missing.yaml")

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "spec.unreadable" {
		t.Fatalf("err - %#v, want a spec.unreadable UserError", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte("shard: 512\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "spec.invalid" {
		t.Fatalf("err = %#v, want a spec.invalid UserError", err)
	}
}
