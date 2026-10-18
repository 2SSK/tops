package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

func newValidateCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:     "validate",
		Short:   "Check cluster.yaml without touching any node",
		GroupID: "spec",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := spec.Load(opts.File)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			problems := spec.Validate(c)
			if len(problems) == 0 {
				_, err := fmt.Fprintf(out, "%s is valid: cluster %s, %d nodes\n", opts.File, c.Cluster, len(c.Nodes))
				return err
			}

			for _, p := range problems {
				if _, err := fmt.Fprintln(out, p); err != nil {
					return err
				}
			}
			return &term.UserError{
				Code: "spec.invalid",
				What: opts.File + " is invalid",
				Fix:  "edit " + opts.File + ", then tops validate",
			}
		},
	}
}
