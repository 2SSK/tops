package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

// mkdirs creates each path under base.
func mkdirs(t *testing.T, base string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Join(base, p), 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

// env returns a getenv that knows only the given variables>
func env(vars map[string]string) func(string) string {
	return func(key string) string {
		return vars[key]
	}
}

func TestFindRoot(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "environments/labs", "base")

	tests := []struct {
		name string
		cwd  string
		vars map[string]string
	}{
		{"at the root", root, nil},
		{"deep inside", filepath.Join(root, "environments", "lab"), nil},
		{"in the base", filepath.Join(root, "base"), nil},
		{"TOPS_ROOT wins", t.TempDir(), map[string]string{"TOPS_ROOT": root}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindRoot(tt.cwd, env(tt.vars))
			if err != nil {
				t.Fatalf("FindRoot: %v", err)
			}
			if got != root {
				t.Errorf("root = %q, want %q", got, root)
			}
		})
	}
}

func TestFindRootNoRoot(t *testing.T) {
	notRoot := t.TempDir()
	mkdirs(t, notRoot, "src")
	if err := os.WriteFile(filepath.Join(notRoot, "src", "environments"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cwd  string
		vars map[string]string
	}{
		{"no environments anywhere", t.TempDir(), nil},
		{"environments is a file", filepath.Join(notRoot, "src"), nil},
		{"TOPS_ROOT without environments", t.TempDir(), map[string]string{"TOPS_ROOT": notRoot}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FindRoot(tt.cwd, env(tt.vars))

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != "spec.no_root" {
				t.Fatalf("err = %#v, want a spec.no_root UserError", err)
			}
		})
	}
}

func TestEnvironments(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "environments/staging", "environments/lab", "environments/Bad_Name")
	if err := os.WriteFile(filepath.Join(root, "environments", "README.md"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Environments(root)
	if err != nil {
		t.Fatalf("Environments: %v", err)
	}
	if want := []string{"lab", "staging"}; !slices.Equal(got, want) {
		t.Errorf("Environments = %q, want %q", got, want)
	}
}

func TestEnvironmentsMissingDir(t *testing.T) {
	if _, err := Environments(t.TempDir()); err == nil {
		t.Error("want an error when environments/ does not exist")
	}
}
