package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/term"
	"github.com/spf13/cobra"
)

// knob is a catalog setting with its apply class. No key is classified yet, so every
// one is unknown, and plan restarts the role for it.
type knob struct {
	catalog.Setting
	Apply string `json:"apply"`
}

// knobsDocument is the --json result of tops knobs.
type knobsDocument struct {
	Kind        string `json:"kind"`
	TopsVersion string `json:"topsVersion"`
	Temporal    string `json:"temporal"`
	Settings    []knob `json:"settings"`
}

func newKnobsCommand(opts *Options) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:     "knobs [text]",
		Short:   "Search the dynamic-config settings of Temporal",
		GroupID: "spec",
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		PreRunE: refuseEnv(opts),
		RunE: func(cmd *cobra.Command, args []string) error {
			minors := catalog.Minors()
			c, err := catalog.Load(minors[len(minors)-1])
			if err != nil {
				return err
			}
			text := strings.Join(args, "")

			// An exact key is that one setting, even when other keys contain it.
			matches := c.Search(text)
			s, exact := c.Lookup(text)
			if exact {
				matches = []catalog.Setting{s}
			}
			knobs := make([]knob, 0, len(matches))
			for _, s := range matches {
				knobs = append(knobs, knob{Setting: s, Apply: "unknown"})
			}
			out := cmd.OutOrStdout()
			if opts.JSON {
				return term.WriteJSON(out, knobsDocument{Kind: "Knobs", TopsVersion: Version, Temporal: c.Temporal, Settings: knobs})
			}
			var page string
			switch {
			case len(knobs) == 0:
				page = noMatch(text, c.Closest(text))
			case exact:
				page = knobPage(knobs[0], c, verbose)
			default:
				page = knobsTable(knobs, c)
			}
			_, err = io.WriteString(out, page)
			return err
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show the default's source and the static hint")
	return cmd
}

// knobsTable is the list of matching settings, one line each.
func knobsTable(knobs []knob, c *catalog.Catalog) string {
	rows := [][]string{{"key", "roles", "apply", "default", "meaning"}}
	for _, k := range knobs {
		rows = append(rows, []string{k.Key, strings.Join(k.Roles, ","), k.Apply, defaultOf(k.Setting), firstSentence(k.Description)})
	}
	return fmt.Sprintf("Temporal %s · %d settings\n\n%s\n%s\n", minorOf(c), len(knobs), table(rows), unknownNote)
}

// knobPage is everything about one setting.
func knobPage(k knob, c *catalog.Catalog, verbose bool) string {
	rows := [][]string{
		{"type", k.Type},
		{"precedence", k.Precedence},
		{"roles", strings.Join(k.Roles, ",")},
		{"apply", k.Apply},
		{"default", defaultOf(k.Setting)},
	}
	if verbose {
		constraints := "none"
		if len(k.Constraints) > 0 {
			constraints = strings.Join(k.Constraints, ",")
		}
		rows = append(rows,
			[]string{"constraints", constraints},
			[]string{"source", k.DefaultExpr},
			[]string{"hint", k.Candidate + " (static, from the source; not a classification)"},
			[]string{"from", c.GeneratedFrom},
		)
	}
	return fmt.Sprintf("%s · Temporal %s\n\n%s\n%s\n", k.Key, minorOf(c), table(rows), indent(k.Description))
}

// noMatch is the answer to a search that matches nothing: the closest keys.
func noMatch(text string, closest []string) string {
	return fmt.Sprintf("no setting matches %q\n  closest: %s\n", text, strings.Join(closest, ", "))
}

const unknownNote = "unknown = not classified yet; plan restarts the role for it. details: tops knobs <key> -v"

// table aligns rows two spaces apart, each indented by two spaces.
func table(rows [][]string) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		// A tabwriter over a strings.Builder never fails to write.
		_, _ = io.WriteString(tw, "  "+strings.Join(r, "\t")+"\n")
	}
	_ = tw.Flush()
	return b.String()
}

// defaultOf is a default as text: its value, a list for a constrained default, or
// its Go source when it is not a c constant.
func defaultOf(s catalog.Setting) string {

	switch v := s.Default.(type) {
	case nil:
		return s.DefaultExpr
	case []any:
		values := make([]string, len(v))
		for i, x := range v {
			values[i] = fmt.Sprint(x)
		}
		return strings.Join(values, ",")
	default:
		return fmt.Sprint(v)
	}
}

// firstSentence is the description up to its first period or line break.
func firstSentence(description string) string {
	s, _, _ := strings.Cut(description, "\n")
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSuffix(s, ".")
}

// indent indents every line of s by two spaces.
func indent(s string) string {
	return "  " + strings.ReplaceAll(s, "\n", "\n ") + "\n"
}

// minorOf is the catalog's Temporal minor: 1.31 for 1.31.3.
func minorOf(c *catalog.Catalog) string {
	return c.Temporal[:strings.LastIndex(c.Temporal, ".")]
}
