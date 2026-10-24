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

// runValidate writes spec to a temporary cluster.yaml and runs "tops validate -f" on it,
// followed by any extra arguments.
func runValidate(t *testing.T, spec string, extra ...string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs(append([]string{"validate", "-f", path}, extra...))
	err := cmd.Execute()
	return out.String(), err
}

const validSpec = `cluster: tops-e2e
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512
persistence:
  postgres: { host: 10.80.0.20, user: temporal }
ssh: { user: deploy }
namespaces: [{ name: default, retention: 72h }]
nodes:
  - { name: vps-01, address: 10.80.0.11 }
  - { name: vps-02, address: 10.80.0.12 }
`

func TestValidateValidSpec(t *testing.T) {
	out, err := runValidate(t, validSpec)
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}
	if !strings.Contains(out, "is valid: cluster tops-e2e, 2 nodes") {
		t.Errorf("output = %q", out)
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

func TestValidateMissingFile(t *testing.T) {
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"validate", "-f", filepath.Join(t.TempDir(), "missing.yaml")})

	if got := term.ExitCode(cmd.Execute()); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
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
		{"invalid", bad, false, []spec.Problem{{Path: "shards", Message: "0 must be between 1 and 16384"}}, 4},
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
