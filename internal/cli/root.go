// Package cli defines the tops command
package cli

import (
	"github.com/spf13/cobra"
)

// Options holds the global flags shared by every command
type Options struct {
	JSON    bool
	NoColor bool
	File    string
}

// NewRootCommand returns the tops root command with all subcommands attached.
// Global flags are parsed into opts.
func NewRootCommand(opts *Options) *cobra.Command {
	root := &cobra.Command{
		Use:           "tops",
		Short:         "Operate a Temporal cluster over SSH",
		Args:          usageArgs(cobra.NoArgs),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	root.SetFlagErrorFunc(usageError)
	root.PersistentFlags().BoolVar(&opts.JSON, "json", false, "machine-readable output")
	root.PersistentFlags().BoolVar(&opts.NoColor, "no-color", false, "plain output")
	root.PersistentFlags().StringVarP(&opts.File, "file", "f", "cluster.yaml", "spec file")

	root.AddGroup(
		&cobra.Group{ID: "spec", Title: "Spec:"},
		&cobra.Group{ID: "change", Title: "Change:"},
		&cobra.Group{ID: "look", Title: "Look:"},
		&cobra.Group{ID: "integrate", Title: "Integrate:"},
	)
	root.SetHelpCommandGroupID("integrate")
	root.SetCompletionCommandGroupID("integrate")

	root.AddCommand(newVersionCommand(opts))
	root.AddCommand(newValidateCommand(opts))

	return root
}
