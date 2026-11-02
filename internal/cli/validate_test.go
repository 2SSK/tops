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

// fixture is the fleet root shared with internal/fleet: environments lab and staging.
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
	writeSecrets(t, dir)
	return run(t, root, append([]string{"-e", "qa", "validate"}, extra...)...)
}

// writeSecrets writes a private secrets.env in the environment directory dir. git
// does not keep 0600, so the fixture has none.
func writeSecrets(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("TOPS_PG_PASSWORD=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixtureFleet copies the fixture to a new directory and gives lab and staging their
// secrets.env.
func fixtureFleet(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"lab", "staging"} {
		writeSecrets(t, filepath.Join(root, "environments", env))
	}
	return root
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
	out, err := run(t, filepath.Join(fixtureFleet(t), "base"), "-e", "lab", "validate")
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}
	if want := "✓ environments/lab is valid: cluster tops-e2e, 3 nodes, 10 roles\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestValidateChecksSecrets(t *testing.T) {
	root := fixtureFleet(t)
	if err := os.Remove(filepath.Join(root, "environments", "lab", "secrets.env")); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, root, "-e", "lab", "validate")

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.What != "cannot read environments/lab/secrets.env" {
		t.Errorf("err = %#v, want the missing secrets.env named", err)
	}
	if got := term.ExitCode(err); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
}

func TestValidateInvalidSpec(t *testing.T) {
	bad := strings.Replace(validSpec, "shards: 512", "shards: 0", 1)
	bad = strings.Replace(bad, "10.80.0.12", "10.80.0.11", 1)

	out, err := runValidate(t, bad)

	if got := term.ExitCode(err); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
	want := "✗ environments/qa/cluster.yaml:3  shards  0 must be between 1 and 16384\n" + "✗ environments/qa/cluster.yaml:10  nodes[1].address  10.80.0.11 is also nodes[0]\n"

	if out != want {
		t.Errorf("output = \n%s\nwant\n%s", out, want)
	}
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.What != "environments/qa is invalid: 2 errors, 0 warnings" {
		t.Errorf("err = %#v, want the errors and warnings counted", err)
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

func TestValidateGetwdFails(t *testing.T) {
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
	out, err := run(t, filepath.Join(fixtureFleet(t), "base"), "-e", "lab", "validate", "--json")
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

func TestValidateSettings(t *testing.T) {
	tests := []struct {
		name, file, data string
		want             string
		wantExit         int
	}{
		{
			"error", "environments/lab/overrides.yaml", "history.cachettl:\n  - value: 15 minutes\n",
			"✗ environments/lab/overrides.yaml:2  history.cachettl[0].value  want a duration like 30s, 15m or 2d, got \"15 minutes\"\n",
			4,
		},
		{
			"error of the layers", "base/history.yaml", "frontend.namespaceRPS:\n  - value: 400\n",
			"✗ base/history.yaml:1  frontend.namespaceRPS  history does not read it (read by frontend); move it to the file of a role that does\n",
			4,
		},
		{
			"warning only", "base/frontend.yaml", "limit.maxIDLength:\n  - value: 500\n",
			"! base/frontend.yaml:1  limit.maxIDLength  also set in base/common.yaml line 2; this file wins for frontend\n" +
				"✓ environments/lab is valid: cluster tops-e2e, 3 nodes, 10 roles\n",
			0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fixtureFleet(t)
			if err := os.WriteFile(filepath.Join(root, tt.file), []byte(tt.data), 0o600); err != nil {
				t.Fatal(err)
			}

			out, err := run(t, root, "-e", "lab", "validate")

			if got := term.ExitCode(err); got != tt.wantExit {
				t.Errorf("exit code = %d, want %d", got, tt.wantExit)
			}
			if out != tt.want {
				t.Errorf("output =\n%s\nwant\n%s", out, tt.want)
			}
			var ue *term.UserError
			if tt.wantExit == 4 && (!errors.As(err, &ue) || ue.What != "environments/lab is invalid: 1 error, 0 warnings") {
				t.Errorf("err = %#v, want 1 error counted", err)
			}
		})
	}
}

func TestValidateJSONWarnings(t *testing.T) {
	root := fixtureFleet(t)
	if err := os.WriteFile(filepath.Join(root, "base", "frontend.yaml"), []byte("limit.maxIDLength:\n  - value: 500\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, root, "-e", "lab", "validate", "--json")
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}

	var doc validationDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	want := []spec.Problem{{File: "base/frontend.yaml", Path: "limit.maxIDLength", Line: 1, Message: "also set in base/common.yaml line 2; this file wins for frontend"}}
	if !doc.OK || !slices.Equal(doc.Warnings, want) {
		t.Errorf("document = %+v, want ok with the warning", doc)
	}
}

func TestValidateNoCatalog(t *testing.T) {
	// 1.30 is a supported release, but its catalog comes in T7.1.
	_, err := runValidate(t, strings.Replace(validSpec, "1.31.3", "1.30.4", 1))
	if err == nil || err.Error() != "no settings catalog for Temporal 1.30" {
		t.Errorf("err = %v, want the missing catalog named", err)
	}
}
