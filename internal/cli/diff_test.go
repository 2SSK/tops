package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// TestDiffGolden compares the output of tops diff lab staging with testdata/diff. HOME is
// fixed because ssh.key and ssh.knownHosts are resolved against it.
func TestDiffGolden(t *testing.T) {
	t.Setenv("HOME", "/home/ops")
	tests := []struct {
		golden string
		args   []string
	}{
		{"lab-staging.txt", []string{"diff", "lab", "staging"}},
		{"lab-staging-v.txt", []string{"diff", "lab", "staging", "-v"}},
		{"lab-staging.json", []string{"diff", "lab", "staging", "--json"}},
		{"staging-lab.txt", []string{"diff", "staging", "lab"}},
	}
	for _, tt := range tests {
		t.Run(tt.golden, func(t *testing.T) {
			out, err := run(t, fixture, tt.args...)
			if got := term.ExitCode(err); got != 2 {
				t.Errorf("exit code = %d, want 2", got)
			}
			var reported *term.Reported
			if !errors.As(err, &reported) {
				t.Errorf("err = %v, want a *term.Reported so main prints nothing more", err)
			}
			checkGolden(t, "testdata/diff", tt.golden, out)
		})
	}
}

func TestDiffJSONDocument(t *testing.T) {
	out, _ := run(t, fixture, "diff", "lab", "staging", "--json")

	var doc envDiffDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out)
	}
	if doc.Kind != "EnvDiff" || doc.From != "lab" || doc.To != "staging" || len(doc.Promotable) != 2 {
		t.Errorf("document = %+v", doc)
	}
}

// The fixture has no secrets.env: a diff connects to nothing, so it reads none.
func TestDiffSame(t *testing.T) {
	out, err := run(t, fixture, "diff", "lab", "lab")
	if err != nil {
		t.Fatalf("diff: %#v", err)
	}
	if want := "lab → lab  (base/ is shared)\n\nno promotable differences\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// edit replaces old with replacement in the file of the fleet at root.
func edit(t *testing.T, root, file, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(filepath.Join(root, file)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s has no %q", file, old)
	}
	write(t, root, file, strings.Replace(string(data), old, replacement, 1))
}

// write writes data as the file of the fleet at root.
func write(t *testing.T, root, file, data string) {
	t.Helper()
	path := filepath.Join(root, file)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil { //nolint:gosec // a test's own temp fleet
		t.Fatal(err)
	}
}

func TestDiffVersion(t *testing.T) {
	root := fixtureFleet(t)
	edit(t, root, "environments/staging/cluster.yaml", "version: 1.31.3", "version: 1.31.2")

	out, err := run(t, root, "diff", "lab", "staging")

	if got := term.ExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if want := "(promote does not write this: tops -e staging upgrade 1.31.3)\n"; !strings.Contains(out, want) {
		t.Errorf("output has no %q:\n%s", want, out)
	}
	if !strings.Contains(out, "  ! temporal.version") {
		t.Errorf("temporal.version is not marked !:\n%s", out)
	}
}

func TestDiffOnlyByDesign(t *testing.T) {
	root := fixtureFleet(t)
	edit(t, root, "environments/staging/cluster.yaml", "shards: 512", "shards: 1024")
	edit(t, root, "environments/lab/cluster.yaml", "  - { name: drills, retention: 24h }\n", "")
	write(t, root, "environments/lab/overrides.yaml", "")

	out, err := run(t, root, "diff", "lab", "staging")
	if err != nil {
		t.Fatalf("diff: by-design differences must not affect the exit code: %#v", err)
	}
	for _, want := range []string{"no promotable differences\n", "\nby design: ", "(tops diff lab staging -v)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "promote with") {
		t.Errorf("nothing to promote, but the output says how:\n%s", out)
	}
}

func TestDiffRefuses(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode string
	}{
		{"-e", []string{"-e", "lab", "diff", "lab", "staging"}, "usage.invalid"},
		{"one environment", []string{"diff", "lab"}, "usage.invalid"},
		{"unknown from", []string{"diff", "nope", "staging"}, "spec.no_env"},
		{"unknown to", []string{"diff", "lab", "nope"}, "spec.no_env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := run(t, fixture, tt.args...)

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.wantCode {
				t.Errorf("err = %#v, want a %s UserError", err, tt.wantCode)
			}
			if got := term.ExitCode(err); got != 4 {
				t.Errorf("exit code = %d, want 4", got)
			}
		})
	}
}

func TestDiffInvalid(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		wreck func(t *testing.T, root string)
	}{
		{"spec of from", "lab", func(t *testing.T, root string) {
			edit(t, root, "environments/lab/cluster.yaml", "shards: 512", "shards: 0")
		}},
		{"overrides of to", "staging", func(t *testing.T, root string) {
			write(t, root, "environments/staging/overrides.yaml", "history.cacheTTL:\n  - value: soon\n")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := fixtureFleet(t)
			tt.wreck(t, root)

			_, err := run(t, root, "diff", "lab", "staging")

			var ue *term.UserError
			if want := "environments/" + tt.env + " is invalid: 1 error"; !errors.As(err, &ue) || ue.Code != "spec.invalid" || ue.What != want {
				t.Fatalf("err = %#v, want spec.invalid %q", err, want)
			}
			if want := "tops -e " + tt.env + " validate shows each problem"; ue.Fix != want {
				t.Errorf("fix = %q, want %q", ue.Fix, want)
			}
		})
	}
}

func TestDiffNoRoot(t *testing.T) {
	_, err := run(t, "/", "diff", "lab", "staging")

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "spec.no_root" {
		t.Errorf("err = %#v, want spec.no_root", err)
	}
}
