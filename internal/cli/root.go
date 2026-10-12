// Package cli defines the tops command
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewRootCommand returns the tops root command with all subcommands attached.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:          "tops",
		Short:        "Operate a Temporal cluster over SSH",
		SilenceUsage: true,
	}

	root.AddGroup(
		&cobra.Group{ID: "spec", Title: "Spec:"},
		&cobra.Group{ID: "change", Title: "Change:"},
		&cobra.Group{ID: "look", Title: "Look:"},
		&cobra.Group{ID: "integrate", Title: "Integrate:"},
	)
	root.SetHelpCommandGroupID("integrate")
	root.SetCompletionCommandGroupID("integrate")

	root.AddCommand(&cobra.Command{
		Use:     "version",
		Short:   "Print the tops version",
		GroupID: "integrate",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "tops dev")
			return err
		},
	})

	return root
}
