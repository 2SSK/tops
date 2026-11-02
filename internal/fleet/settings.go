package fleet

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// Catalog is the settings catalog of the spec's Temporal minor: 1.31 for 1.31.3.
func (e *Env) Catalog() (*catalog.Catalog, error) {
	v := e.Cluster.Temporal.Version
	return catalog.Load(v[:max(strings.LastIndex(v, "."), 0)])
}

// settingsReader reads settings files one afer another and keeps the first error, so
// Settings reads the list of files (the errWriter of go.dev/blog/errors-are-values).
type settingsReader struct {
	root     string
	c        *catalog.Catalog
	problems []spec.Problem
	err      error
}

// Settings reads the environments dynamic-config files and checks each against c:
// base/common.yaml, base/<role>.yaml for every role, then its overrides.yaml. A file that
// does not exist sets nothing. The problems caome in that order of files.
func (e *Env) Settings(c *catalog.Catalog) (settings.Layers, []spec.Problem, error) {
	r := &settingsReader{root: e.Root, c: c}
	l := settings.Layers{Common: r.read(filepath.Join("base", "common.yaml")), Roles: map[string]settings.File{}}
	for _, role := range spec.Roles {
		l.Roles[role] = r.read(filepath.Join("base", role+".yaml"))
	}
	l.Overrides = r.read(filepath.Join("environments", e.Name, "overrides.yaml"))
	return l, r.problems, r.err
}

// read parses file, named relative to the fleet root. After an error it reads nothing.
func (r *settingsReader) read(file string) settings.File {
	if r.err != nil {
		return nil
	}

	data, err := os.ReadFile(filepath.Clean(filepath.Join(r.root, file)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		r.err = &term.UserError{Code: "spec.unreadable", What: "cannot read " + file, Why: err.Error(), Fix: "check the file"}
		return nil
	}
	f, problems, err := settings.Parse(file, data, r.c)
	r.problems = append(r.problems, problems...)
	r.err = err
	return f
}
