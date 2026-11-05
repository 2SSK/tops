package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// emptyFleet is a new fleet repo: mkdir environments.
func emptyFleet(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "environments"), 0o750); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInit(t *testing.T) {
	root := emptyFleet(t)

	out, err := run(t, root, "init", "qa")
	if err != nil {
		t.Fatalf("init: %#v", err)
	}
	want := `✓ created environments/qa/cluster.yaml
✓ created environments/qa/overrides.yaml
✓ created environments/qa/secrets.env (0600)
✓ created base/common.yaml
✓ created base/frontend.yaml
✓ created base/history.yaml
✓ created base/matching.yaml
✓ created base/worker.yaml
next: edit environments/qa/cluster.yaml, then tops -e qa validate
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}

	// base/ is shared: the next environment finds it complete.
	out, err = run(t, root, "init", "staging")
	if err != nil {
		t.Fatalf("init: %#v", err)
	}
	want = `✓ created environments/staging/cluster.yaml
✓ created environments/staging/overrides.yaml
✓ created environments/staging/secrets.env (0600)
· base/ already complete
next: edit environments/staging/cluster.yaml, then tops -e staging validate
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

// The way the operator meets it: init, validate names the missing secret, set it, valid.
func TestInitThenValidate(t *testing.T) {
	root := emptyFleet(t)
	if _, err := run(t, root, "init", "qa"); err != nil {
		t.Fatalf("init: %#v", err)
	}

	_, err := run(t, root, "-e", "qa", "validate")
	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "secrets.invalid" {
		t.Fatalf("validate before the secret is set: err = %#v, want secrets.invalid", err)
	}

	write(t, root, "environments/qa/secrets.env", "TOPS_PG_PASSWORD=x\n")
	out, err := run(t, root, "-e", "qa", "validate")
	if err != nil {
		t.Fatalf("validate: %#v", err)
	}
	if want := "✓ environments/qa is valid: cluster qa, 2 nodes, 8 roles\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestInitJSON(t *testing.T) {
	out, err := run(t, emptyFleet(t), "init", "qa", "--json")
	if err != nil {
		t.Fatalf("init: %#v", err)
	}

	var doc initDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	if doc.Kind != "Init" || doc.Env != "qa" || !slices.Contains(doc.Created, "environments/qa/secrets.env") {
		t.Errorf("document = %+v", doc)
	}
}

func TestInitRefuses(t *testing.T) {
	root := emptyFleet(t)
	if _, err := run(t, root, "init", "qa"); err != nil {
		t.Fatalf("init: %#v", err)
	}

	tests := []struct {
		name string
		dir  string
		args []string
		code string
	}{
		{"existing environment", root, []string{"init", "qa"}, "spec.env_exists"},
		{"-e", root, []string{"-e", "qa", "init", "staging"}, "usage.invalid"},
		{"no name", root, []string{"init"}, "usage.invalid"},
		{"bad name", root, []string{"init", "../qa"}, "usage.invalid"},
		{"no fleet root", "/", []string{"init", "qa"}, "spec.no_root"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := run(t, tt.dir, tt.args...)

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.code {
				t.Errorf("err = %#v, want a %s UserError", err, tt.code)
			}
			if got := term.ExitCode(err); got != 4 {
				t.Errorf("exit code = %d, want 4", got)
			}
		})
	}
}
