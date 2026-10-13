package cli

import (
	"github.com/2SSK/tops/internal/term"
	"github.com/spf13/cobra"
)

// usageError turns a cobra parsing error into a UserError that exits with code 4.
func usageError(cmd *cobra.Command, err error) error {
	return &term.UserError{
		Code: "usage.invalid",
		What: err.Error(),
		Fix:  cmd.CommandPath() + " --help",
	}
}

// usageArgs wraps an argument validator so that its errors are usage errors.
func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return usageError(cmd, err)
		}
		return nil
	}
}
