package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/fleet"
	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// envDiffDocument is the --json result of tops diff.
type envDiffDocument struct {
	Kind        string `json:"kind"`
	TopsVersion string `json:"topsVersion"`
	From        string `json:"from"`
	To          string `json:"to"`
	fleet.EnvDiff
}

func newDiffCommand(opts *Options) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:     "diff <from> <to>",
		Short:   "Compare two environments offline",
		GroupID: "spec",
		Args:    usageArgs(cobra.ExactArgs(2)),
		PreRunE: refuseEnv(opts),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := fleetRoot(opts)
			if err != nil {
				return err
			}
			from, err := loadValid(root, args[0])
			if err != nil {
				return err
			}
			to, err := loadValid(root, args[1])
			if err != nil {
				return err
			}
			d, err := fleet.Diff(from, to)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if opts.JSON {
				err = term.WriteJSON(out, envDiffDocument{Kind: "EnvDiff", TopsVersion: Version, From: from.Name, To: to.Name, EnvDiff: d})
			} else {
				_, err = io.WriteString(out, diffPage(from.Name, to.Name, d, verbose, StyleFor(out, *opts)))
			}
			if err != nil {
				return err
			}
			// Differences are an outcome, already printed: exit 2 and nothing more.
			if len(d.Promotable) > 0 {
				return &term.Reported{Err: &term.UserError{Code: "pending.differences", What: from.Name + " and " + to.Name + " differ"}}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "list the by-design differences")
	return cmd
}

// loadValid loads the environment name and refuses it when validate would. A diff needs
// no secrets, so it does not read secrets.env.
func loadValid(root, name string) (*fleet.Env, error) {
	env, err := fleet.Load(root, name)
	if err != nil {
		return nil, err
	}
	errs, _, err := checkSettings(env, spec.Validate(env.Cluster))
	if err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, &term.UserError{
			Code: "spec.invalid",
			What: fmt.Sprintf("%s is invalid: %s", filepath.Dir(env.File), count(len(errs), "error")),
			Fix:  "tops -e " + name + " validate shows each problem",
		}
	}
	return env, nil
}

// diffPage is the result of tops diff: the promotable differences, the by-design ones
// listed with -v and counted without, and the next command.
func diffPage(from, to string, d fleet.EnvDiff, verbose bool, st term.Style) string {
	page := from + " → " + to + "  (base/ is shared)\n\n"
	if len(d.Promotable) == 0 {
		page += "no promotable differences\n"
	} else {
		page += "promotable\n" + table(diffRows(d.Promotable, to, st))
	}
	switch {
	case len(d.ByDesign) == 0:
	case verbose:
		page += "\nby design\n" + table(diffRows(d.ByDesign, to, st))
	default:
		page += fmt.Sprintf("\nby design: %s (tops diff %s %s -v)\n", count(len(d.ByDesign), "difference"), from, to)
	}
	if len(d.Promotable) > 0 {
		page += "promote with: tops promote " + from + " " + to + "\n"
	}

	return page
}

// diffRows is one row per difference: its mark and path, then both valies. promote
// never writes temporal.version, so that rows says what to run instead.
func diffRows(diffs []fleet.Difference, to string, st term.Style) [][]string {
	rows := make([][]string, len(diffs))
	for i, d := range diffs {
		rows[i] = []string{st.Mark(diffMark(d)) + " " + d.Path, diffValue(d.From), diffValue(d.To)}
		if d.Path == "temporal.version" {
			rows[i] = append(rows[i], fmt.Sprintf("(promote does not write this: tops -e %s upgrade %v)", to, d.From))
		}
	}
	return rows
}

// diffMark is what promote would do to a path: add it, remove it or change it.
func diffMark(d fleet.Difference) term.Symbol {
	switch {
	case d.Path == "temporal.version":
		return term.Warn
	case d.To == nil:
		return term.Add
	case d.From == nil:
		return term.Remove
	default:
		return term.Change
	}
}

// diffValue is a value as text, - when the environment does not have it or it is empty.
func diffValue(v any) string {
	switch v := v.(type) {
	case []settings.Value:
		return values(v)
	case string:
		if v != "" {
			return v
		}
	}
	return "-"
}
