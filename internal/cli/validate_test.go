package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// runValidate writes spec to a temporary cluster.yaml and runs "tops validate -f" on it.
func runValidate(t *testing.T, spec string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := NewRootCommand(&Options{})
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"validate", "-f", path})
	err := cmd.Execute()
	return out.String(), err
}

const validSpec = `cluster: tscale-lab
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512
profile: 2c4g
persistence:
  postgres: { host: 10.78.0.20, user: temporal }
ssh: { user: deploy }
namespaces: [{ name: default, retention: 72h }]
nodes:
  - { name: tscale-01, address: 10.78.0.11 }
  - { name: tscale-02, address: 10.78.0.12 }
`

func TestValidateValidSpec(t *testing.T) {
	out, err := runValidate(t, validSpec)
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}
	if !strings.Contains(out, "is valid: cluster tscale-lab, 2 nodes") {
		t.Errorf("output = %q", out)
	}
}

func TestValidateInvalidSpec(t *testing.T) {
	bad := strings.Replace(validSpec, "shards: 512", "shards: 0", 1)
	bad = strings.Replace(bad, "10.78.0.12", "10.78.0.11", 1)

	out, err := runValidate(t, bad)

	if got := term.ExitCode(err); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
	want := "shards: 0 must be between 1 and 16384\nnodes[1].address: 10.78.0.11 is also nodes[0]\n"
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
