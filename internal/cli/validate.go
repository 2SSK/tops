package cli

import (
	"fmt"
	"io"

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

			c := env.Cluster
			out := cmd.OutOrStdout()
			problems := spec.Validate(c)
			if opts.JSON {
				return writeValidation(out, env, problems)
			}
			if len(problems) == 0 {
				_, err := fmt.Fprintf(out, "%s is valid: cluster %s, %d nodes\n", env.File, c.Cluster, len(c.Nodes))
				return err
			}

			for _, p := range problems {
				if _, err := fmt.Fprintln(out, p); err != nil {
					return err
				}
			}
			return invalidSpec(env)
		},
	}
}

// validationDocument is the --json result of tops validate
type validationDocument struct {
	Kind        string         `json:"kind"`
	TopsVersion string         `json:"topsVersion"`
	OK          bool           `json:"ok"`
	Errors      []spec.Problem `json:"errors"`
	Warnings    []spec.Problem `json:"warnings"`
}

// writeValidation writes the Validation document. For an invalid spec it still
// exits 4, but main must not print a second document, so the error is Reported.
func writeValidation(w io.Writer, env *fleet.Env, problems []spec.Problem) error {
	doc := validationDocument{
		Kind:        "Validation",
		TopsVersion: Version,
		OK:          len(problems) == 0,
		Errors:      problems,
		Warnings:    []spec.Problem{},
	}
	if doc.Errors == nil {
		doc.Errors = []spec.Problem{}
	}

	if err := term.WriteJSON(w, doc); err != nil {
		return err
	}
	if !doc.OK {
		return &term.Reported{Err: invalidSpec(env)}
	}

	return nil
}

// invalidSpec is the error for a spec with problems: exit 4, code spec.invalid.
func invalidSpec(env *fleet.Env) error {
	return &term.UserError{
		Code: "spec.invalid",
		What: env.File + " is invalid",
		Fix:  "edit " + env.File + ", then tops -e " + env.Name + " validate",
	}
}
