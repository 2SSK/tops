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
	rejected, err := h.validateUnits(ctx, run, v)
	if err != nil {
		return wire.ValidateResult{}, err
	}
	r.Rejected = append(r.Rejected, rejected...)
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

// why a command failed: render-config's error, which spans lines when YAML does not decode,
// else the last line of stderr (log lines comes first), on one line; its exit status when it
// said nothing. Never "", which would read as accepted.
func why(stderr string, exit *exec.ExitError) string {
	s := strings.TrimSpace(stderr)
	if i := strings.LastIndex(s, "Unable to load configuration:"); i >= 0 {
		s = s[i:]
	} else if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if s = strings.Join(strings.Fields(s), " "); s != "" {
		return s
	}
	return exit.Error()
}

// mask replaces the values of the role's secret variables in msg with ***.yaml.v3 quotes a
// value longer than 10 characters as its first 7 and "...", so that form goes too.
func mask(msg string, f wire.RoleFiles) string {
	for _, kv := range f.Env {
		key, value, _ := strings.Cut(kv, "=")
		if value == "" || !slices.Contains(f.Secrets, key) {
			continue
		}
		msg = strings.ReplaceAll(msg, value, "***")
		if len(value) > 10 {
			msg = strings.ReplaceAll(msg, value[:7]+"...", "***")
		}
	}

	return msg
}

// unitDir is where the unit and its drop-ins are installed; messages name them there.
const unitDir = "/etc/systemd/system/"

// validateUnits checks the unit and each role's drop-in as systemd will load them, written to
// dir as they would be to /etc/systemd/system: with only dir on the unit path, nothing already
// on the node joins in. systemd ignores a setting it cannot parse with a warning and exits 0,
// so any line it prints is a rejection.
func (h Host) validateUnits(ctx context.Context, dir string, v wire.Validate) ([]wire.Rejection, error) {
	files := map[string]string{filepath.Join(dir, "temporal@.service"): v.Unit}
	args := []string{"verify", "--man=no", "--recursive-errors=no"}
	for _, f := range v.Roles {
		unit := filepath.Join(dir, "temporal@"+f.Role+".service")
		files[filepath.Join(unit+".d", "limits.conf")] = f.Limits
		args = append(args, unit) // the instance: systemd loads the template and the drop-in for it
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("validate units: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return nil, fmt.Errorf("validate units: %w", err)
		}
	}

	cmd := exec.CommandContext(ctx, filepath.Join(h.Root, "usr/bin/systemd-analyze"), args...) //nolint:gosec	// a fixed binary, paths we built

	cmd.Env = []string{"SYSTEMD_UNIT_PATH=" + dir} // no trailing ":": /etc/systemd/system stays out
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return nil, fmt.Errorf("run systemd-analyze verify: %w", err)
	}
	return unitRejections(string(out), dir, v.Roles, exit), nil
}

// unitRejections sorts systemd's lines by the file they are about, a role's drop-in, else the
// unit, and names it where it will be installed. Each line counts once: systemd loads the
// template again for every instance. A failure without a line is the unit's.
func unitRejections(out, dir string, roles []wire.RoleFiles, exit *exec.ExitError) []wire.Rejection {
	problems := map[string][]string{} // by role, "" for the unit
	for line := range strings.Lines(out) {
		line = strings.ReplaceAll(strings.TrimSpace(line), dir+"/", unitDir)
		if line == "" {
			continue
		}
		role := dropInRole(line, roles)
		if !slices.Contains(problems[role], line) {
			problems[role] = append(problems[role], line)
		}
	}
	if exit != nil && len(problems) == 0 {
		problems[""] = []string{exit.Error()}
	}
	var rejected []wire.Rejection
	if p := problems[""]; len(p) > 0 {
		rejected = append(rejected, wire.Rejection{File: "unit", Message: strings.Join(p, "; ")})
	}
	for _, f := range roles {
		if p := problems[f.Role]; len(p) > 0 {
			rejected = append(rejected, wire.Rejection{Role: f.Role, File: "limits", Message: strings.Join(p, "; ")})
		}
	}
	return rejected
}

// dropInRole is the role whose drop-in a line of systemd's is about; "" is the unit.
func dropInRole(line string, roles []wire.RoleFiles) string {
	for _, f := range roles {
		if strings.HasPrefix(line, unitDir+"temporal@"+f.Role+".service.d/") {
			return f.Role
		}
	}
	return ""
}
