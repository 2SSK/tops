// Package cli defines the tops command
package cli

import (
	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/helper"
)

// Options holds the global flags shared by every command, and the process
// environment, injected so that tests never touch the real one.
type Options struct {
	JSON    bool
	NoColor bool
	Env     string

	Getenv     func(string) string
	Getwd      func() (string, error)
	Geteuid    func() int
	Node       helper.Host                       // the matchine tops node reads
	Passphrase func(path string) ([]byte, error) // asks for ssh.key's passphrase; nil without a terminal
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
	root.PersistentFlags().StringVarP(&opts.Env, "env", "e", "", "environment under environments/")

	root.AddGroup(
		&cobra.Group{ID: "spec", Title: "Spec:"},
		&cobra.Group{ID: "integrate", Title: "Integrate:"},
	)
	root.SetHelpCommandGroupID("integrate")
	root.SetCompletionCommandGroupID("integrate")

	root.AddCommand(newVersionCommand(opts))
	root.AddCommand(newValidateCommand(opts))
	root.AddCommand(newKnobsCommand(opts))
	root.AddCommand(newDiffCommand(opts))
	root.AddCommand(newInitCommand(opts))
	root.AddCommand(newNodeCommand(opts))

	// Cobra adds completion only to a root that has subcommands, so this comes last.
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.PersistentPreRunE = refuseEnv(opts)
		}
	}

	return root
}
