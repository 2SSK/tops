package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// fixture is the fleet root shared wither internal/fleet: environments lab and staging.
const fixture = "../fleet/testdata/fleet"

// run executes tops with args as if started in dir, with only LANG=C.UTF-8 set.
func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := NewRootCommand(&Options{
		Getenv: func(k string) string {
			if k == "LANG" {
				return "C.UTF-8"
			}
			return ""
		},
		Getwd: func() (string, error) { return dir, nil },
	})
	cmd.SetOut(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// runValidate writes spec as environments/qa/cluster.yaml of a new fleet and runs
// "tops -e qa validate" there, followed by any extra arguments.
func runValidate(t *testing.T, spec string, extra ...string) (string, error) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "environments", "qa")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cluster.yaml"), []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	return run(t, root, append([]string{"-e", "qa", "validate"}, extra...)...)
}

const validSpec = `cluster: tops-e2e
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512
persistence:
  postgres: { host: 10.80.0.20, user: temporal }
ssh: { user: deploy }
namespaces: [{ name: default, retention: 72h }]
nodes:
  - { name: vps-01, address: 10.80.0.11, roles: [frontend, history, matching, worker] }
  - { name: vps-02, address: 10.80.0.12, roles: [frontend, history, matching, worker] }
`

func TestValidateFixture(t *testing.T) {
	out, err := run(t, filepath.Join(fixture, "base"), "-e", "lab", "validate")
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}
	if want := "✓ environments/lab is valid: cluster tops-e2e, 3 nodes, 10 roles\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestValidateInvalidSpec(t *testing.T) {
	bad := strings.Replace(validSpec, "shards: 512", "shards: 0", 1)
	bad = strings.Replace(bad, "10.80.0.12", "10.80.0.11", 1)

	out, err := runValidate(t, bad)

	if got := term.ExitCode(err); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
	want := "shards: 0 must be between 1 and 16384\nnodes[1].address: 10.80.0.11 is also nodes[0]\n"
	if out != want {
		t.Errorf("output = \n%s\nwant\n%s", out, want)
	}
}

func TestValidateNeedsEnv(t *testing.T) {
	_, err := run(t, fixture, "validate")

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "usage.invalid" {
		t.Fatalf("err = %#v, want a usage.invalid UserError", err)
	}
	if ue.Why != "environments: lab, staging" {
		t.Errorf("why = %q, want the environments listed", ue.Why)
	}
}

func TestValidateLoadErrors(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		args     []string
		wantCode string
	}{
		{"no fleet root", "/", []string{"-e", "lab", "validate"}, "spec.no_root"},
		{"no fleet root and no -e", "/", []string{"validate"}, "spec.no_root"},
		{"unknown environment", fixture, []string{"-e", "nope", "validate"}, "spec.no_env"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := run(t, tt.dir, tt.args...)

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.wantCode {
				t.Errorf("err = %#v, want a %s UserError", err, tt.wantCode)
			}
		})
	}
}

func TesetValidateGetwdFails(t *testing.T) {
	cmd := NewRootCommand(&Options{
		Getenv: func(string) string { return "" },
		Getwd:  func() (string, error) { return "", errors.New("getwd: no such file or directory") },
	})
	cmd.SetArgs([]string{"-e", "lab", "validate"})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "find the fleet root") {
		t.Errorf("err = %v", err)
	}
}

func TestValidateJSON(t *testing.T) {
	bad := strings.Replace(validSpec, "shards: 512", "shards: 0", 1)

	tests := []struct {
		name     string
		spec     string
		wantOK   bool
		wantErrs []spec.Problem
		wantExit int
	}{
		{"valid", validSpec, true, []spec.Problem{}, 0},
		{"invalid", bad, false, []spec.Problem{{Path: "shards", Line: 3, Message: "0 must be between 1 and 16384"}}, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runValidate(t, tt.spec, "--json")

			if got := term.ExitCode(err); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d", got, tt.wantExit)
			}
			var reported *term.Reported
			if err != nil && !errors.As(err, &reported) {
				t.Errorf("err = %v, want a *term.Reported so main prints nothing more", err)
			}

			var doc validationDocument
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
			}
			if doc.Kind != "Validation" || doc.OK != tt.wantOK || !slices.Equal(doc.Errors, tt.wantErrs) {
				t.Errorf("document = %+v", doc)
			}
			if !strings.Contains(out, `"errors":[`) {
				t.Errorf("errors must be a JSON array, never null: %s", out)
			}
		})
	}
}

func TestValidateJSONNamesTheEnvironment(t *testing.T) {
	out, err := run(t, fixture, "-e", "lab", "validate", "--json")
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}

	var doc validationDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	if doc.Env != "lab" || doc.Cluster != "tops-e2e" {
		t.Errorf("env = %q, cluster = %q, want lab and tops-e2e", doc.Env, doc.Cluster)
	}
}
