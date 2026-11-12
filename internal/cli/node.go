package cli

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/helper"
	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// newNodeCommand returns tops node <op>, the helper: the workstation's tops runs it as
// root on a node over SSH, one op per run. It is hidden because people never type it.
func newNodeCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:    "node <op>",
		Short:  "Run one helper op on this node, reading the request on stdin",
		Hidden: true,
		Args:   usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			// An error leaves exe empty: hello then fails with a response that say so.
			exe, _ := os.Executable()
			code := helper.Run(cmd.Context(), wire.Op(args[0]), helper.Env{
				Host:    opts.Node,
				Root:    opts.Geteuid() == 0,
				Version: Version,
				Exe:     exe,
				Stdin:   cmd.InOrStdin(),
				Stdout:  cmd.OutOrStdout(),
				Log:     slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), nil)),
			})
			if code == helper.ExitOK {
				return nil
			}
			// The response on stdout already says why: main prints nothing more.
			return &term.Reported{Err: &term.Exit{Code: code}}
		},
	}
}
