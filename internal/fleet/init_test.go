package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// newRoot is an empty fleet root.
func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "environments"), 0o750); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestInit(t *testing.T) {
	root := newRoot(t)

	created, err := Init(root, "qa")
	if err != nil {
		t.Fatalf("Init: %#v", err)
	}

	want := []string{
		"environments/qa/cluster.yaml",
		"environments/qa/overrides.yaml",
		"environments/qa/secrets.env",
		"base/common.yaml",
		"base/frontend.yaml",
		"base/history.yaml",
		"base/matching.yaml",
		"base/worker.yaml",
	}
	if !slices.Equal(created, want) {
		t.Errorf("created = %v, want %v", created, want)
	}
	info, err := os.Stat(filepath.Join(root, "environments", "qa", "secrets.env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("secrets.env mode = %o, want 600", got)
	}
}

// The template must stay valid as the rules grow, and name its environment.
func TestInitTemplateIsValid(t *testing.T) {
	root := newRoot(t)
	if _, err := Init(root, "qa"); err != nil {
		t.Fatal(err)
	}

	env := load(t, root, "qa")
	if env.Cluster.Cluster != "qa" {
		t.Errorf("cluster = %q, want qa", env.Cluster.Cluster)
	}
	if problems := spec.Validate(env.Cluster); len(problems) > 0 {
		t.Errorf("the template has problems: %v", problems)
	}
	c, err := env.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	_, problems, err := env.Settings(c)
	if err != nil || len(problems) > 0 {
		t.Errorf("settings: err %v, problems %v", err, problems)
	}

	// secrets.env names the key but does not set it, so validate cannot pass yet.
	var ue *term.UserError
	if _, err := env.ReadSecrets(); !errors.As(err, &ue) || !strings.Contains(ue.Error()+ue.Why+ue.Fix, "TOPS_PG_PASSWORD") {
		t.Errorf("ReadSecrets err = %#v, want the missing key named", err)
	}
}

func TestInitKeepsBase(t *testing.T) {
	root := newRoot(t)
	if err := os.Mkdir(filepath.Join(root, "base"), 0o750); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(root, "base", "common.yaml")
	const kept = "limit.maxIDLength:\n  - value: 500\n"
	if err := os.WriteFile(common, []byte(kept), 0o600); err != nil {
		t.Fatal(err)
	}

	created, err := Init(root, "qa")
	if err != nil {
		t.Fatal(err)
	}

	if slices.Contains(created, "base/common.yaml") {
		t.Errorf("created = %v, but base/common.yaml existed", created)
	}
	if !slices.Contains(created, "base/worker.yaml") {
		t.Errorf("created = %v, want the missing base/worker.yaml", created)
	}
	data, err := os.ReadFile(filepath.Clean(common))
	if err != nil || string(data) != kept {
		t.Errorf("base/common.yaml = %q, %v; init must not overwrite it", data, err)
	}
}

func TestInitRefuses(t *testing.T) {
	root := newRoot(t)
	if _, err := Init(root, "qa"); err != nil {
		t.Fatal(err)
	}
	secrets := filepath.Join(root, "environments", "qa", "secrets.env")
	if err := os.WriteFile(secrets, []byte("TOPS_PG_PASSWORD=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, env, code string
	}{
		{"existing environment", "qa", "spec.env_exists"},
		{"path traversal", "../qa", "usage.invalid"},
		{"uppercase", "QA", "usage.invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Init(root, tt.env)

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.code {
				t.Errorf("err = %#v, want a %s UserError", err, tt.code)
			}
		})
	}

	data, err := os.ReadFile(filepath.Clean(secrets))
	if err != nil || string(data) != "TOPS_PG_PASSWORD=x\n" {
		t.Errorf("secrets.env = %q, %v; the refusal must leave it alone", data, err)
	}
}

// No environments/, or a file where base/ should be: nothing init can repair.
func TestInitErrors(t *testing.T) {
	if _, err := Init(t.TempDir(), "qa"); err == nil {
		t.Error("no environments/: err = nil")
	}

	baseIsFile := newRoot(t)
	if err := os.WriteFile(filepath.Join(baseIsFile, "base"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(baseIsFile, "qa"); err == nil {
		t.Error("base is a file: err = nil")
	}
}
