package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/term"
)

func TestCatalog(t *testing.T) {
	env, err := Load(fixture, "lab")
	if err != nil {
		t.Fatal(err)
	}
	c, err := env.Catalog()
	if err != nil || c.Temporal != "1.31.3" {
		t.Errorf("catalog = %v, %v; want Temporal 1.31.3", c, err)
	}

	env.Cluster.Temporal.Version = "latest"
	if _, err := env.Catalog(); err == nil {
		t.Error("err = nil for version latest, want one")
	}
}

func TestSettings(t *testing.T) {
	env, err := Load(fixture, "lab")
	if err != nil {
		t.Fatal(err)
	}
	c, err := env.Catalog()
	if err != nil {
		t.Fatal(err)
	}

	l, problems, err := env.Settings(c)
	if err != nil || len(problems) > 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}

	tests := []struct {
		layer     settings.File
		key, file string
	}{
		{l.Common, "limit.maxIDLength", "base/common.yaml"},
		{l.Roles["history"], "history.cacheTTL", "base/history.yaml"},
		{l.Overrides, "frontend.namespaceRPS", "environments/lab/overrides.yaml"},
	}
	for _, tt := range tests {
		if e, ok := tt.layer[tt.key]; !ok || e.File != tt.file {
			t.Errorf("%s = %+v, want it from %s", tt.key, e, tt.file)
		}
	}
	if len(l.Roles["frontend"]) != 0 {
		t.Errorf("frontend = %v, want nothing from the empty base/frontend.yaml", l.Roles["frontend"])
	}
}

func TestSettingsNoOverrides(t *testing.T) {
	env, err := Load(fixture, "staging")
	if err != nil {
		t.Fatal(err)
	}
	c, err := env.Catalog()
	if err != nil {
		t.Fatal(err)
	}

	l, _, err := env.Settings(c)
	if err != nil || len(l.Overrides) != 0 {
		t.Errorf("overrides = %v, err %v; a missing overrides.yaml sets nothing", l.Overrides, err)
	}
}

// labWith copies the fixture and writes data as file, relative to the copy's root.
func labWith(t *testing.T, file, data string) *Env {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	env, err := Load(root, "lab")
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestSettingsProblems(t *testing.T) {
	env := labWith(t, "base/matching.yaml", "matching.numTaskqueueReadPartitions:\n  - value: four\n")
	c, err := env.Catalog()
	if err != nil {
		t.Fatal(err)
	}

	_, problems, err := env.Settings(c)
	if err != nil || len(problems) != 1 || problems[0].File != "base/matching.yaml" || problems[0].Line != 2 {
		t.Errorf("problems = %+v, err %v; want one, on base/matching.yaml line 2", problems, err)
	}
}

func TestSettingsErrors(t *testing.T) {
	notYAML := labWith(t, "base/common.yaml", "a: [\n")
	unreadable := labWith(t, "base/worker.yaml", "")
	// A directory where the file should be: it exists, but cannot be read.
	path := filepath.Join(unreadable.Root, "base", "worker.yaml")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		env  *Env
		code string
	}{
		{"not YAML", notYAML, "spec.invalid"},
		{"unreadable", unreadable, "spec.unreadable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := tt.env.Catalog()
			if err != nil {
				t.Fatal(err)
			}

			_, _, err = tt.env.Settings(c)

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.code {
				t.Errorf("err = %#v, want a %s UserError", err, tt.code)
			}
		})
	}
}
