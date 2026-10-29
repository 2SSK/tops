package fleet

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"

	"github.com/2SSK/tops/internal/secret"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// Secrets are the values in secrets.env, by key.
type Secrets map[string]secret.Value

// knownSecrets are the keys secrets.env may set.
var knownSecrets = []string{"TOPS_PG_PASSWORD"}

// secretValue is what a value may contain: it goes into a YAML string and a systemd env file.
var secretValue = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// parseSecrets reads secrets.env; file names it in errors. One KEY=VALUE per line, # comment
// lines and blank lines. Values are taken literally: no quotes, no export, no $ expansion.
// An error names the line and the key, never the value.
func parseSecrets(file string, data []byte) (Secrets, error) {
	s := Secrets{}
	lineOf := map[string]int{}

	for i, line := range strings.Split(string(data), "\n") {
		n := i + 1
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		switch {
		case !ok:
			return nil, invalidLine(file, n, "expected KEY=VALUE")
		case !slices.Contains(knownSecrets, key):
			return nil, invalidLine(file, n, fmt.Sprintf("%q is not a known key (%s)", key, strings.Join(knownSecrets, ", ")))
		case lineOf[key] != 0:
			return nil, invalidLine(file, n, fmt.Sprintf("%v is also set on line %v", key, lineOf[key]))
		case !secretValue.MatchString(value):
			return nil, invalidLine(file, n, fmt.Sprintf("%v may only contain A-Z a-z 0-9 . _ ~ -", key))
		}
		s[key] = secret.New(value)
		lineOf[key] = n
	}
	return s, nil
}

// requireSecrets are they keys the spec needs: the password of its datastore.
func requireSecrets(c *spec.Cluster) []string {
	if c.Persistence.Postgres != nil {
		return []string{"TOPS_PG_PASSWORD"}
	}
	return nil
}

// checkRequired returns an error for the first key c needs that s does not set.
func checkRequired(file string, s Secrets, c *spec.Cluster) error {
	for _, key := range requireSecrets(c) {
		if _, ok := s[key]; !ok {
			return &term.UserError{
				Code: "secrets.invalid",
				What: file + " has no " + key,
				Why:  "persistence.postgres needs its password",
				Fix:  "add a line " + key + "=<password> to " + file,
			}
		}
	}
	return nil
}

// invalidLine is the error for line n of a secrets file.
func invalidLine(file string, n int, why string) error {
	return &term.UserError{
		Code: "secrets.invalid",
		What: fmt.Sprintf("%s line %d", file, n),
		Why:  why,
		Fix:  "edit " + file + ": one KEY=VALUE per line, no quotes, no export",
	}
}

// ReadSecrets reads secrets.env next to cluster.yaml: it must be a regular file with
// mode 0600 owned by the current user, parse, and set every key the spec needs.
func (e *Env) ReadSecrets() (Secrets, error) {
	file := filepath.Join(filepath.Dir(e.File), "secrets.env")
	path := filepath.Join(e.Dir, "secrets.env")

	info, err := os.Lstat(path)
	if err != nil {
		return nil, unreadableSecrets(file, err)
	}
	if err := checkPrivate(file, info, os.Getuid()); err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, unreadableSecrets(file, err)
	}
	s, err := parseSecrets(file, data)
	if err != nil {
		return nil, err
	}

	return s, checkRequired(file, s, e.Cluster)
}

// checkPrivate returns a secrets.mode error unless info is a regular file with mode
// 0600 owned by uid.
func checkPrivate(file string, info fs.FileInfo, uid int) error {
	if !info.Mode().IsRegular() {
		return notPrivate(file, "it is not a regular file (symlinks are not followed)", "replace it with a regular file")
	}

	if perm := info.Mode().Perm(); perm != 0o600 {
		return notPrivate(file, fmt.Sprintf("mode %04o, must be 0600", perm), "chmod 600 "+file)
	}

	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
		return notPrivate(file, fmt.Sprintf("it is owned by another user, must be yours (uid %d)", uid), fmt.Sprintf("chown %d %s", uid, file))
	}

	return nil
}

// notPrivate is the error for a secrets file others could read or replace.
func notPrivate(file, why, fix string) error {
	return &term.UserError{
		Code: "secrets.mode",
		What: file + " is not private",
		Why:  why,
		Fix:  fix,
	}
}

// unreadableSecrets is the error for a secrets file that cannot be read. The why
// drops the path from err: file already names it, relative to the fleet root.
func unreadableSecrets(file string, err error) error {
	why := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		why = pe.Err.Error()
	}

	return &term.UserError{
		Code: "secrets.invalid",
		What: "cannot read " + file,
		Why:  why,
		Fix:  "create it with mode 0600 and a line TOPS_PG_PASSWORD=<password>",
	}
}
