package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// Env is one environment loaded from the fleet root.
type Env struct {
	Name    string
	Dir     string // the environment directory under the fleet root
	File    string // cluster.yaml, relative to the fleet root, for messages
	Cluster *spec.Cluster
}

// Load reads environments/<name>/cluster.yaml under root, applies the defaults and
// resolves ssh.key and ssh.knownHosts against the environment directory.
func Load(root, name string) (*Env, error) {
	dir := filepath.Join(root, "environments", name)
	if !envName.MatchString(name) || !isDir(dir) {
		return nil, noEnv(root, name)
	}

	file := filepath.Join("environments", name, "cluster.yaml")
	data, err := os.ReadFile(filepath.Clean(filepath.Join(root, file)))
	if err != nil {
		return nil, &term.UserError{
			Code: "spec.unreadable",
			What: "cannot read " + file,
			Why:  err.Error(),
			Fix:  "check the file",
		}
	}

	c, err := spec.Parse(file, data)
	if err != nil {
		return nil, err
	}
	spec.Defaults(c)

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve ~ in %s: %w", file, err)
	}
	c.SSH.Key = resolvePath(c.SSH.Key, dir, home)
	c.SSH.KnownHosts = resolvePath(c.SSH.KnownHosts, dir, home)

	return &Env{Name: name, Dir: dir, File: file, Cluster: c}, nil
}

// resolvePath expands a leading ~ to home and makes a relative path relative to dir.
// An empty path stays empty: ssh.key has no default (the agent is used).
func resolvePath(p, dir, home string) string {
	switch {
	case p == "":
		return ""
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	case filepath.IsAbs(p):
		return p
	default:
		return filepath.Join(dir, p)
	}
}

// noEnv is the error for an environment that does not exist under root.
func noEnv(root, name string) error {
	return &term.UserError{
		Code: "spec.no_env",
		What: fmt.Sprintf("environment %q not found", name),
		Why:  Available(root),
		Fix:  "pass -e with one of the environments above",
	}
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// Available lists the environments under root for an error's why line.
func Available(root string) string {
	envs, err := Environments(root)
	if err != nil || len(envs) == 0 {
		return "no environments found"
	}
	return "environments: " + strings.Join(envs, ", ")
}
