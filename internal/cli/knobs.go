package cli

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/settings"
	"github.com/2SSK/tops/internal/term"
)

// knob is a catalog setting with its class: apply, and the evidence for it. With -e it
// also has the environment's layers.
type knob struct {
	catalog.Setting
	catalog.Class
	Layers *settings.Trace `json:"layers,omitempty"`
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
		RunE: func(cmd *cobra.Command, args []string) error {
			c, layers, err := knobsCatalog(cmd, opts)
			if err != nil {
				return err
			}
			text := strings.Join(args, " ")

			// An exact key is that one setting, even when other keys contain it.
			matches := c.Search(text)
			s, exact := c.Lookup(text)
			if exact {
				matches = []catalog.Setting{s}
			}
			knobs := make([]knob, 0, len(matches))
			for _, s := range matches {
				k := knob{Setting: s, Class: c.Class(s.Key)}
				if layers != nil {
					t := layers.Trace(s, c)
					k.Layers = &t
				}
				knobs = append(knobs, k)
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
				page = knobPage(knobs[0], c, verbose, opts.Env)
			default:
				page = knobsTable(knobs, c)
			}
			_, err = io.WriteString(out, page)
			return err
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "show the source, the static hint and the evidence")
	return cmd
}

// knobsCatalog is the catalog knobs reads, and with -e the environment's layers: the
// catalog of the spec's minor. Without -e it is the newest supported minor, and no layers.
func knobsCatalog(cmd *cobra.Command, opts *Options) (*catalog.Catalog, *settings.Layers, error) {
	if opts.Env == "" {
		minors := catalog.Minors()
		c, err := catalog.Load(minors[len(minors)-1])
		return c, nil, err
	}
	env, err := loadEnv(cmd, opts)
	if err != nil {
		return nil, nil, err
	}
	c, err := env.Catalog()
	if err != nil {
		return nil, nil, err
	}
	layers, problems, err := env.Settings(c)
	if err != nil {
		return nil, nil, err
	}
	// Layers with problems would show a value other than the one tops applies.
	if errs, _ := layers.Check(c); len(problems)+len(errs) > 0 {
		return nil, nil, &term.UserError{
			Code: "spec.invalid",
			What: filepath.Dir(env.File) + " has invalid settings",
			Fix:  "tops -e " + env.Name + " validate shows each problem",
		}
	}
	return c, &layers, nil
}

// knobsTable is the list of matching settings, one line each.
func knobsTable(knobs []knob, c *catalog.Catalog) string {
	rows := [][]string{{"key", "roles", "apply", "default", "meaning"}}
	for _, k := range knobs {
		rows = append(rows, []string{k.Key, strings.Join(k.Roles, ","), k.Apply, defaultOf(k.Setting), firstSentence(k.Description)})
	}
	note := ""
	if slices.ContainsFunc(knobs, func(k knob) bool { return k.Apply == "unknown" }) {
		note = "\n" + unknownNote + "\n"
	}
	return fmt.Sprintf("Temporal %s · %d settings\n\n%s%s", minorOf(c), len(knobs), table(rows), note)
}

// knobPage is everything about one setting, and with -e its layers in env.
func knobPage(k knob, c *catalog.Catalog, verbose bool, env string) string {
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
		if k.Evidence != "" {
			rows = append(rows, []string{"evidence", k.Evidence})
		}
		if k.Lab != "" {
			rows = append(rows, []string{"lab", k.Lab})
		}
	}
	page := fmt.Sprintf("%s · Temporal %s\n\n%s\n%s\n", k.Key, minorOf(c), table(rows), indent(k.Description))
	if k.Layers != nil {
		page += layersTable(k, env)
	}
	return page
}

// layersTable is where k gets its value in env: the default, each file, then what each
// role that reads it runs, and from which file.
func layersTable(k knob, env string) string {
	t := k.Layers
	readers := settings.Readers(k.Setting)
	rows := [][]string{{"default", defaultOf(k.Setting)}, {"base/common.yaml", values(t.Common)}}
	for _, role := range readers {
		rows = append(rows, []string{"base/" + role + ".yaml", values(t.Roles[role])})
	}
	rows = append(rows, []string{filepath.Join("environments", env, "overrides.yaml"), values(t.Override)})
	for _, role := range readers {
		e := t.Effective[role]
		value := values(e.Values)
		if e.File == "default" {
			value = defaultOf(k.Setting)
		}
		rows = append(rows, []string{role + " runs", value + "  from " + e.File})
	}
	return "layers in " + env + "\n\n" + table(rows) + "\n"
}

// values is a key's list as text, - when the file does not set it:
// 400 (namespace=drills), 2400.
func values(list []settings.Value) string {
	if len(list) == 0 {
		return "-"
	}
	items := make([]string, len(list))
	for i, v := range list {
		items[i] = fmt.Sprint(v.Value)
		if len(v.Constraints) > 0 {
			var cs []string
			for _, name := range slices.Sorted(maps.Keys(v.Constraints)) {
				cs = append(cs, fmt.Sprintf("%s=%v", name, v.Constraints[name]))
			}
			items[i] += " (" + strings.Join(cs, ", ") + ")"
		}
	}
	return strings.Join(items, ", ")
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
// its Go source when it is not a Go constant.
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
	return "  " + strings.ReplaceAll(s, "\n", "\n  ") + "\n"
}

// minorOf is the catalog's Temporal minor: 1.31 for 1.31.3.
func minorOf(c *catalog.Catalog) string {
	return c.Temporal[:strings.LastIndex(c.Temporal, ".")]
}
