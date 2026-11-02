package cli

import (
	"cmp"
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

func newValidateCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:     "validate",
		Short:   "Check an environment without touching any node",
		GroupID: "spec",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := loadEnv(cmd, opts)
			if err != nil {
				return err
			}
			errs, warnings, err := check(env)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if opts.JSON {
				return writeValidation(out, env, errs, warnings)
			}
			st := StyleFor(out, *opts)
			if err := writeProblems(out, st.Mark(term.Fail), env, errs); err != nil {
				return err
			}
			if err := writeProblems(out, st.Mark(term.Warn), env, warnings); err != nil {
				return err
			}
			if len(errs) > 0 {
				return invalidSpec(env, errs, warnings)
			}
			_, err = fmt.Fprintln(out, st.Mark(term.OK), summary(env))
			return err
		},
	}
}

// check runs every offline check of env: cluster.yaml, secrets.env, then the
// dynamic-config files with the catalog of the spec's Temporal minor. Warnings do not
// make env invalid.
func check(env *fleet.Env) (errs, warnings []spec.Problem, err error) {
	errs = spec.Validate(env.Cluster)
	// The keys secrets.env must set depend on the spec, so read it only for a valid one.
	if len(errs) == 0 {
		if _, err := env.ReadSecrets(); err != nil {
			return nil, nil, err
		}
	}

	if slices.ContainsFunc(errs, func(p spec.Problem) bool {
		return p.Path == "temporal.version"
	}) {
		return errs, nil, nil
	}

	c, err := env.Catalog()
	if err != nil {
		return nil, nil, err
	}
	layers, problems, err := env.Settings(c)
	if err != nil {
		return nil, nil, err
	}
	layerErrs, warnings := layers.Check(c)
	return slices.Concat(errs, problems, layerErrs), warnings, nil
}

// writeProblems prints each problem as: mark file:line  path  message. A problem
// without a file is one of cluster.yaml.
func writeProblems(w io.Writer, mark string, env *fleet.Env, problems []spec.Problem) error {
	for _, p := range problems {
		if _, err := fmt.Fprintf(w, "%s %s:%d  %s  %s\n", mark, cmp.Or(p.File, env.File), p.Line, p.Path, p.Message); err != nil {
			return err
		}
	}
	return nil
}

// validationDocument is the --json result of tops validate
type validationDocument struct {
	Kind        string         `json:"kind"`
	TopsVersion string         `json:"topsVersion"`
	Env         string         `json:"env"`
	Cluster     string         `json:"cluster"`
	OK          bool           `json:"ok"`
	Errors      []spec.Problem `json:"errors"`
	Warnings    []spec.Problem `json:"warnings"`
}

// writeValidation writes the Validation document. For an invalid spec it still
// exits 4, but main must not print a second document, so the error is Reported.
func writeValidation(w io.Writer, env *fleet.Env, errs, warnings []spec.Problem) error {
	doc := validationDocument{
		Kind:        "Validation",
		TopsVersion: Version,
		Env:         env.Name,
		Cluster:     env.Cluster.Cluster,
		OK:          len(errs) == 0,
		// Empty lists are [], never null.
		Errors:   append([]spec.Problem{}, errs...),
		Warnings: append([]spec.Problem{}, warnings...),
	}
	if err := term.WriteJSON(w, doc); err != nil {
		return err
	}
	if !doc.OK {
		return &term.Reported{Err: invalidSpec(env, errs, warnings)}
	}
	return nil
}

// summary is the line for a valid environment: environments/lab is valid: cluster
// tops-e2e, 3 nodes, 10 roles. Roles count every role on every node.
func summary(env *fleet.Env) string {
	c := env.Cluster
	roles := 0

	for _, n := range c.Nodes {
		roles += len(n.Roles)
	}

	return fmt.Sprintf("%s is valid: cluster %s, %d nodes, %d roles", filepath.Dir(env.File), c.Cluster, len(c.Nodes), roles)
}

// invalidSpec is the error for an environment with problems: exit 4, code spec.invalid.
func invalidSpec(env *fleet.Env, errs, warnings []spec.Problem) error {
	return &term.UserError{
		Code: "spec.invalid",
		What: fmt.Sprintf("%s is invalid: %s, %s", filepath.Dir(env.File), count(len(errs), "error"), count(len(warnings), "warning")),
		Fix:  "edit the files above, then tops -e " + env.Name + " validate",
	}
}

// count is n of noun: 1 error, 2 errors.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
