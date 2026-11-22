package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/wire"
)

// runDir holds each run's scratch files, /run/tops/<run-id>: a tmpfs, gone at reboot.
const runDir = "/run/tops"

const codeNotStaged = "release.not_staged"

// runIDName is a run id as a directory name: "r-20261023T153012Z-9c1e" in a real run.
var runIDName = regexp.MustCompile(`^r-[A-Za-z0-9-]+$`)

// roleName is a role as a directory name.
var roleName = regexp.MustCompile(`^[a-z]+$`)

// envKey is a variable name systemd passes from an EnvironmentFile.
var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Validate runs the staged release's temporal-server on each role's files, in
// /run/tops/<run-id>/<role>/, and removes the run's directory when it is done, whatever happens.
// A file the binary refuses is a Rejection, not an error: the workstation decides what to do.
func (h Host) Validate(ctx context.Context, runID string, v wire.Validate) (wire.ValidateResult, error) {
	bin := filepath.Join(h.Root, releasesDir, v.Version, "temporal-server")
	if _, err := os.Stat(bin); errors.Is(err, fs.ErrNotExist) {
		return wire.ValidateResult{}, &wire.Error{Code: codeNotStaged, Message: v.Version + " is not staged", Detail: "no " + filepath.Join(releasesDir, v.Version, "temporal-server")}
	}
	run := filepath.Join(h.Root, runDir, runID)
	if err := os.MkdirAll(run, 0o700); err != nil {
		return wire.ValidateResult{}, fmt.Errorf("create %s: %w", filepath.Join(runDir, runID), err)
	}
	defer func() { _ = os.RemoveAll(run) }()

	r := wire.ValidateResult{Rejected: []wire.Rejection{}}
	for _, f := range v.Roles {
		rejected, err := validateRole(ctx, bin, filepath.Join(run, f.Role), f)
		if err != nil {
			return wire.ValidateResult{}, err
		}
		r.Rejected = append(r.Rejected, rejected...)
	}
	return r, nil
}

// checkValidate refuses what could make validate write outside its run directory.
func checkValidate(runID string, v wire.Validate) error {
	if !runIDName.MatchString(runID) {
		return fmt.Errorf("run id %q is not a directory name", runID)
	}
	if !versionName.MatchString(v.Version) {
		return fmt.Errorf("release version %q is not x.y.z", v.Version)
	}
	var seen []string
	for _, f := range v.Roles {
		if !roleName.MatchString(f.Role) || slices.Contains(seen, f.Role) {
			return fmt.Errorf("role %q is not a name, or appears twice", f.Role)
		}
		seen = append(seen, f.Role)
		for _, kv := range f.Env {
			if key, _, _ := strings.Cut(kv, "="); !envKey.MatchString(key) {
				return fmt.Errorf("%s: env %q is not KEY=VALUE", f.Role, key)
			}
		}
	}
	return nil
}

// validateRole writes one role's files where render-config looks for them, production.yaml
// in a directory of their own, and runs both checks.
func validateRole(ctx context.Context, bin, dir string, f wire.RoleFiles) ([]wire.Rejection, error) {
	config := filepath.Join(dir, "production.yaml")
	dynamic := filepath.Join(dir, "dynamicconfig.yaml")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("validate %s: %w", f.Role, err)
	}
	for path, content := range map[string]string{config: f.Config, dynamic: f.DynamicConfig} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return nil, fmt.Errorf("validate %s: %w", f.Role, err)
		}
	}

	var rejected []wire.Rejection
	msg, err := renderConfig(ctx, bin, dir, f.Env)
	if err != nil {
		return nil, fmt.Errorf("validate %s: %w", f.Role, err)
	}
	if msg != "" {
		rejected = append(rejected, wire.Rejection{Role: f.Role, File: "config", Message: mask(msg, f)})
	}
	if msg, err = validateDynamicConfig(ctx, bin, dynamic); err != nil {
		return nil, fmt.Errorf("validate %s: %w", f.Role, err)
	}
	if msg != "" {
		rejected = append(rejected, wire.Rejection{Role: f.Role, File: "dynamicConfig", Message: mask(msg, f)})
	}
	return rejected, nil
}

// renderConfig loads the role's config as temporal-server start would, with exactly the
// environment systemd gives the role, and returns why it refused, or "". Its stdout is the
// rendered config with the password in it: it goes nowhere.
func renderConfig(ctx context.Context, bin, dir string, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "--config", dir, "--env", "production", "render-config") //nolint:gosec // the staged release's binary, a path we built
	cmd.Env = append([]string{}, env...)                                                          // never nil: nil would pass the helper's own environment
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return why(stderr.String(), exit), nil // "Unable to load configuration: ...", after its log lines
	case err != nil:
		return "", fmt.Errorf("run temporal-server render-config: %w", err)
	}
	return "", nil
}

// validateDynamicConfig checks a dynamic-config file against the release's keys and types,
// and returns its errors and warnings, or "". 1.31.3 exits 0 even when it finds errors: only
// the "error:" and "warning:" lines on stdout say so.
func validateDynamicConfig(ctx context.Context, bin, file string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, "validate-dynamic-config", file) //nolint:gosec // as above
	cmd.Env = []string{}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var problems []string
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "error: ") || strings.HasPrefix(line, "warning: ") {
			problems = append(problems, line)
		}
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		problems = append(problems, why(stderr.String(), exit))
	case err != nil:
		return "", fmt.Errorf("run temporal-server validate-dynamic-config: %w", err)
	}
	return strings.Join(problems, "; "), nil
}

// why a command failed: the last line of its stderr, after any log lines, or its exit status
// when it said nothing. Never "", which would read as accepted.
func why(stderr string, exit *exec.ExitError) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return exit.Error()
}

// mask replaces the values of the role's secret variables in msg with ***.
func mask(msg string, f wire.RoleFiles) string {
	for _, kv := range f.Env {
		key, value, _ := strings.Cut(kv, "=")
		if value != "" && slices.Contains(f.Secrets, key) {
			msg = strings.ReplaceAll(msg, value, "***")
		}
	}
	return msg
}
