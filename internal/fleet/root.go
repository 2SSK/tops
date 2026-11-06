// Package fleet finds the fleet repo and loads one environment from it.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/2SSK/tops/internal/term"
)

// envName is what an environment directory may be called.
var envName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

// FindRoot returns the fleet root: $TOPS_ROOT if set, else the nearest directory
// from cwd upwards that contains environments.
func FindRoot(cwd string, getenv func(string) string) (string, error) {
	if root := getenv("TOPS_ROOT"); root != "" {
		if !hasEnvironments(root) {
			return "", noRoot("TOPS_ROOT="+root+" has no environments/", "correct TOPS_ROOT, or unset it")
		}
		return root, nil
	}

	for dir := cwd; ; dir = filepath.Dir(dir) {
		if hasEnvironments(dir) {
			return dir, nil
		}
		if dir == filepath.Dir(dir) {
			return "", noRoot("no environments/ in "+cwd+" or any parent", "run tops inside the fleet repo, set TOPS_ROOT, or start one here: tops init <env>")
		}
	}
}

// InitRoot returns where init creates an environment: the fleet root, or cwd when there
// is none, which starts a new fleet repo there. A $TOPS_ROOT without environments/ is
// still refused: it is more likely a typo than a place for a new fleet.
func InitRoot(cwd string, getenv func(string) string) (string, error) {
	root, err := FindRoot(cwd, getenv)
	if err != nil && getenv("TOPS_ROOT") == "" {
		return cwd, nil
	}
	return root, err
}

// Environments returns the environment name under the root, sorted.
func Environments(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "environments"))
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}

	var envs []string
	for _, e := range entries {
		if e.IsDir() && envName.MatchString(e.Name()) {
			envs = append(envs, e.Name())
		}
	}
	return envs, nil
}

func hasEnvironments(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "environments"))
	return err == nil && info.IsDir()
}

func noRoot(why, fix string) error {
	return &term.UserError{
		Code: "spec.no_root",
		What: "no fleet repo here",
		Why:  why,
		Fix:  fix,
	}
}
