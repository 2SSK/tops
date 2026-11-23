package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/2SSK/tops/internal/wire"
)

// codeSchemaFailed: temporal-sql-tool exited non-zero; the detail is its error, secrets masked.
const codeSchemaFailed = "schema.failed"

// sqlPlugin is the tool's driver for PostgreSQL 12 and later
const sqlPlugin = "postgres12"

var (
	// sqlName is a database or user as the spec allows it: never a flag.
	sqlName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	// schemaName is one of the schemas the tool embeds for PostgreSQL.
	schemaName = regexp.MustCompile(`^postgresql/v12/(temporal|visibility)$`)
	// hostName is a DNS name or an IPv4 address.
	hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)
	// ansiColor is what the tool's log puts around its levels, even when it is not on a terminal.
	ansiColor = regexp.MustCompile("\x1b\\[[0-9;]*m")
)

// Schema creates each target database the workstation found missing, versions it and
// migrates it to the newest schema the staged release's temporal-sql-tool embeds. Each step
// is a no-op once done: setup-schema skips a database past 0.0, update-schema has nothing
// to apply, so a second run changes nothing.
func (h Host) Schema(ctx context.Context, s wire.Schema) error {
	tool := filepath.Join(h.Root, releasesDir, s.Release, "temporal-sql-tool")
	if _, err := os.Stat(tool); errors.Is(err, fs.ErrNotExist) {
		return &wire.Error{
			Code:    codeNotStaged,
			Message: s.Release + " is not staged",
			Detail:  "no " + filepath.Join(releasesDir, s.Release, "temporal-sql-tool"),
		}
	}
	for _, t := range s.Targets {
		steps := [][]string{{"setup-schema", "--version", "0.0"}, {"update-schema", "--schema-name", t.SchemaName}}
		if t.Create {
			steps = slices.Insert(steps, 0, []string{"create-database", "--defaultdb", s.AdminDatabase})
		}
		for _, step := range steps {
			if err := sqlTool(ctx, tool, s, t.Database, step); err != nil {
				return err
			}
		}
	}
	return nil
}

// sqlTool runs one step of temporal-sql-tool on one database. The password goes in its
// environment, never argv, which every user on the node can read in ps; its stdout goes
// nowhere.
func sqlTool(ctx context.Context, tool string, s wire.Schema, database string, step []string) error {
	args := []string{"--endpoint", s.Host, "--port", strconv.Itoa(s.Port), "--user", s.User, "--database", database, "--plugin", sqlPlugin}
	if s.TLS {
		args = append(args, "--tls")
	}
	cmd := exec.CommandContext(ctx, tool, append(args, step...)...) //nolint:gosec // the staged release's tool, arguments checkSchema allowed
	cmd.Env = []string{"SQL_PASSWORD=" + s.Password}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		detail := why(ansiColor.ReplaceAllString(stderr.String(), ""), exit)
		return &wire.Error{Code: codeSchemaFailed, Message: step[0] + " " + database + " failed", Detail: maskValue(detail, s.Password)}
	case err != nil:
		return fmt.Errorf("run temporal-sql-tool %s: %w", step[0], err)
	}
	return nil
}

// maskValue replaces a secret in msg with ***, also as the tool puts it in a connection URL.
func maskValue(msg, secret string) string {
	if secret == "" {
		return msg
	}
	return strings.ReplaceAll(strings.ReplaceAll(msg, secret, "***"), url.QueryEscape(secret), "***")
}

// checkSchema refuses what would reach the tool's argv as anything but the value it is: a
// name that starts with "-" would be a flag.
func checkSchema(s wire.Schema) error {
	switch {
	case !versionName.MatchString(s.Release):
		return fmt.Errorf("release version %q is not x.y.z", s.Release)
	case !hostName.MatchString(s.Host) || s.Port < 1 || s.Port > 65535:
		return fmt.Errorf("%q port %d is not a host and port", s.Host, s.Port)
	case !sqlName.MatchString(s.User) || !sqlName.MatchString(s.AdminDatabase):
		return fmt.Errorf("user %q or admin database %q is not a name", s.User, s.AdminDatabase)
	}
	return checkTargets(s.Targets)
}

// checkTargets refuses a database that is not a name or appear twice, and a schema the
// tool does not embed for PostgreSQL.
func checkTargets(targets []wire.SchemaTarget) error {
	var seen []string
	for _, t := range targets {
		if !sqlName.MatchString(t.Database) || slices.Contains(seen, t.Database) || !schemaName.MatchString(t.SchemaName) {
			return fmt.Errorf("database %q with schema %q is not a target, or appears twice", t.Database, t.SchemaName)
		}
		seen = append(seen, t.Database)
	}
	return nil
}
