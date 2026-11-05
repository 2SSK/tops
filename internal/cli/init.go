package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/term"
	"github.com/spf13/cobra"
)

// initDocument is the --json result of tops init.
type initDocument struct {
	Kind        string   `json:"kind"`
	TopsVersion string   `json:"topsVersion"`
	Env         string   `json:"env"`
	Created     []string `json:"created"`
}

func newInitCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:     "init <env>",
		Short:   "Create a new environment from a commented template",
		GroupID: "spec",
		Args:    usageArgs(cobra.ExactArgs(1)),
		PreRunE: refuseEnv(opts),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := fleetRoot(opts)
			if err != nil {
				return err
			}
			created, err := fleet.Init(root, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				return term.WriteJSON(out, initDocument{Kind: "Init", TopsVersion: Version, Env: args[0], Created: created})
			}
			_, err = fmt.Fprint(out, initPage(args[0], created, StyleFor(out, *opts)))
			return err
		},
	}
}

// initPage lists what init created, says when base/ needed nothing, and names the next step.
func initPage(env string, created []string, st term.Style) string {
	var b strings.Builder
	for _, path := range created {
		note := ""
		if filepath.Base(path) == "secrets.env" {
			note = " (0600)"
		}
		fmt.Fprintf(&b, "%s created %s%s\n", st.Mark(term.OK), path, note)
	}
	inBase := func(path string) bool { return filepath.Dir(path) == "base" }
	if !slices.ContainsFunc(created, inBase) {
		fmt.Fprintf(&b, "%s base/ already complete\n", st.Mark(term.Skip))
	}
	fmt.Fprintf(&b, "next: edit %s, then tops -e %s validate\n", filepath.Join("environments", env, "cluster.yaml"), env)
	return b.String()
}
