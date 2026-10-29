package fleet

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

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
