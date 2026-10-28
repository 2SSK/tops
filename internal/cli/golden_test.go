package cli

import (
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/term"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/bad")

// bad holds the lab spec with one thing broken per <rule>.yaml, and its golden output.
const bad = "testdata/bad"

// TestValidateGolden runs tops validate on every <rule>.yaml in bad and compares stdout
// with <rule>.txt and <rule>.json. After a deliberate change, rewrite them with
// go test ./internal/cli -run Golden -update, and review the diff before committing.
func TestValidateGolden(t *testing.T) {
	fsys := os.DirFS(bad)
	specs, err := fs.Glob(fsys, "*.yaml")
	if err != nil || len(specs) == 0 {
		t.Fatalf("no fixtures in %s: %v", bad, err)
	}

	for _, file := range specs {
		rule := strings.TrimSuffix(file, ".yaml")
		t.Run(rule, func(t *testing.T) {
			data, err := fs.ReadFile(fsys, file)
			if err != nil {
				t.Fatal(err)
			}

			human, err := runValidate(t, string(data))
			if got := term.ExitCode(err); got != 4 {
				t.Errorf("exit code = %d, want 4", got)
			}
			checkGolden(t, fsys, rule+".txt", human)

			js, _ := runValidate(t, string(data), "--json")
			checkGolden(t, fsys, rule+".json", js)

			var doc validationDocument
			if err := json.Unmarshal([]byte(js), &doc); err != nil {
				t.Fatalf("stdout is not one JSON document: %v", err)
			}

			for _, p := range doc.Errors {
				if p.Line < 1 {
					t.Errorf("%s has no line", p)
				}
			}
		})
	}
}

// checkGolden compares got with the golden file name in fsys, or writes it with -update.
func checkGolden(t *testing.T, fsys fs.FS, name, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(filepath.Join(bad, name), []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("%v; create it with -update", err)
	}
	if got != string(want) {
		t.Errorf("%s differs:\n got:\n%s\nwant:\n%s", name, got, want)
	}
}
