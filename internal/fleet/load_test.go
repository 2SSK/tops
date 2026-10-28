package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// fixture is a fleet root with environments lab and staging.
const fixture = "testdata/fleet"

func TestLoad(t *testing.T) {
	env, err := Load(fixture, "lab")
	if err != nil {
		t.Fatalf("Load: %#v", err)
	}

	if env.Name != "lab" || env.File != "environments/lab/cluster.yaml" {
		t.Errorf("env = %q, file = %q", env.Name, env.File)
	}
	c := env.Cluster
	if c.Cluster != "tops-e2e" || len(c.Nodes) != 3 {
		t.Errorf("cluster = %q with %d nodes", c.Cluster, len(c.Nodes))
	}
	if c.SSH.Port != 22 {
		t.Errorf("ssh.port = %d, want the default 22 (Load must apply Defaults)", c.SSH.Port)
	}
}

func TestLoadResolvePaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	env, err := Load(fixture, "staging")
	if err != nil {
		t.Fatalf("Load: %#v", err)
	}

	ssh := env.Cluster.SSH
	if want := filepath.Join(fixture, "environments", "staging", "keys", "id_ed25519"); ssh.Key != want {
		t.Errorf("ssh.key = %q, want %q (relative to the environment)", ssh.Key, want)
	}
	if want := filepath.Join(home, ".ssh", "known_hosts"); ssh.KnownHosts != want {
		t.Errorf("ssh.knownHosts = %q, want %q (the default, ~ expanded)", ssh.KnownHosts, want)
	}
}

func TestLoadNoEnv(t *testing.T) {
	for _, name := range []string{"stagin", "", "../lab", "Lab"} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(fixture, name)

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != "spec.no_env" {
				t.Fatalf("err = %#v, want a spec.no_env UserError", err)
			}
			if ue.Why != "environments: lab, staging" {
				t.Errorf("why = %q, want the environments listed", ue.Why)
			}
		})
	}
}

func TestLoadErrorsNameTheFile(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		wantCode string
		wantWhat string
	}{
		{"missing file", "", "spec.unreadable", "cannot read environments/qa/cluster.yaml"},
		{"not YAML", "cluster: [\n", "spec.invalid", "environments/qa/cluster.yaml is invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			mkdirs(t, root, "environments/qa")
			if tt.spec != "" {
				if err := os.WriteFile(filepath.Join(root, "environments", "qa", "cluster.yaml"), []byte(tt.spec), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err := Load(root, "qa")

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.wantCode {
				t.Fatalf("err = %#v, want a %s UserError", err, tt.wantCode)
			}
			if ue.What != tt.wantWhat {
				t.Errorf("what = %q, want %q", ue.What, tt.wantWhat)
			}
		})
	}
}

func TestNoEnvWithoutEnvironments(t *testing.T) {
	err := noEnv(t.TempDir(), "lab")

	var ue *term.UserError
	if !errors.As(err, &ue) || !strings.Contains(ue.Why, "no environments") {
		t.Errorf("err = %#v", err)
	}
}

func TestResolvePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"", ""},
		{"~", "/home/op"},
		{"~/.ssh/known_hosts", "/home/op/.ssh/known_hosts"},
		{"/etc/ssh/known_hosts", "/etc/ssh/known_hosts"},
		{"keys/id_ed25519", "/fleet/environments/lab/keys/id_ed25519"},
		{"../../keys/id_ed25519", "/fleet/keys/id_ed25519"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := resolvePath(tt.path, "/fleet/environments/lab", "/home/op"); got != tt.want {
				t.Errorf("resolvePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
