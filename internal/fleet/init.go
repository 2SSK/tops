package fleet

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

//go:embed template/cluster.yaml
var clusterTemplate string

// secretsTemplate names the keys secrets.env needs: validate refuses it until they are set.
//
//go:embed template/secrets.env
var secretsTemplate string

// emptyMap is a settings file that sets nothing.
const emptyMap = "{}\n"

// Init creates environment name under root: its cluster.yaml, overrides.yaml and
// secrets.env, and any base/ file that is missing. It never overwrites a file, and
// returns what it created, relative to root.
func Init(root, name string) ([]string, error) {
	if !envName.MatchString(name) {
		return nil, &term.UserError{
			Code: "usage.invalid",
			What: fmt.Sprintf("%q is not an environment name", name),
			Fix:  "use lowercase letters, digits and -, starting with a letter",
		}
	}
	dir := filepath.Join("environments", name)
	if err := os.Mkdir(filepath.Join(root, dir), 0o750); errors.Is(err, fs.ErrExist) {
		return nil, &term.UserError{
			Code: "spec.env_exists",
			What: dir + " already exists",
			Why:  "init never overwrites",
			Fix:  "edit it, or pick another name",
		}
	} else if err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "base"), 0o750); err != nil {
		return nil, fmt.Errorf("create base: %w", err)
	}

	files := []struct{ path, data string }{
		{filepath.Join(dir, "cluster.yaml"), strings.ReplaceAll(clusterTemplate, "__ENV__", name)},
		{filepath.Join(dir, "overrides.yaml"), emptyMap},
		{filepath.Join(dir, "secrets.env"), secretsTemplate},
		{filepath.Join("base", "common.yaml"), emptyMap},
	}
	for _, role := range spec.Roles {
		files = append(files, struct{ path, data string }{filepath.Join("base", role+".yaml"), emptyMap})
	}

	created := []string{}
	for _, f := range files {
		ok, err := createFile(filepath.Join(root, f.path), f.data)
		if err != nil {
			return created, fmt.Errorf("create %s: %w", f.path, err)
		}
		if ok {
			created = append(created, f.path)
		}
	}
	return created, nil
}

// createFile writes data to a new private file, and reports false if path already exists.
// O_EXCL makes that one atomic step: no moment exists in which a file could be overwritten.
func createFile(path, data string) (bool, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = f.WriteString(data)
	return true, errors.Join(err, f.Close())
}
