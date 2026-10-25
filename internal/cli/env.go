package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/term"
)

// loadEnv finds the fleet root and loads the environment named by -e.
// Without -e it is a usage error that lists environments: there is no default.
func loadEnv(cmd *cobra.Command, opts *Options) (*fleet.Env, error) {
	cwd, err := opts.Getwd()
	if err != nil {
		return nil, fmt.Errorf("find the fleet root: %w", err)
	}

	root, err := fleet.FindRoot(cwd, opts.Getenv)
	if err != nil {
		return nil, err
	}

	if opts.Env == "" {
		return nil, &term.UserError{
			Code: "usage.invalid",
			What: cmd.CommandPath() + " needs an environment",
			Why:  fleet.Available(root),
			Fix:  "pass -e with one of the environments above",
		}
	}
	return fleet.Load(root, opts.Env)
}

// refuseEnv is a PersistentPreRunE for commands that take no environment.
func refuseEnv(opts *Options) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		if opts.Env != "" {
			return &term.UserError{
				Code: "usage.invalid",
				What: cmd.CommandPath() + " takes no environment",
				Fix:  "drop -e " + opts.Env,
			}
		}
		return nil
	}
}
