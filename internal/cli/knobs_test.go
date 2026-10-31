package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/term"
)

func TestKnobsSearch(t *testing.T) {
	out, err := run(t, t.TempDir(), "knobs", "cacheTTL")
	if err != nil {
		t.Fatalf("knobs: %#v", err)
	}

	for _, want := range []string{
		"Temporal 1.31 · 7 settings\n\n",
		"\n  key ",
		"\n  history.cacheTTL                           history  unknown  1h       HistoryCacheTTL is TTL of history cache\n",
		"\n\nunknown = not classified yet; plan restarts the role for it. details: tops knobs <key> -v\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output has no %q:\n%s", want, out)
		}
	}
}

func TestKnobsExactKey(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		wants []string
	}{
		{"exact key", []string{"knobs", "History.CacheTTL"}, []string{
			"history.cacheTTL · Temporal 1.31\n\n",
			"  roles       history\n",
			"  default     1h\n",
			"\n  HistoryCacheTTL is TTL of history cache. Requires service restart to take effect.\n",
		}},
		{"verbose", []string{"knobs", "history.cacheTTL", "-v"}, []string{
			"  constraints  none\n",
			"  source       time.Hour\n",
			"  hint         restart (static, from the source; not a classification)\n",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, t.TempDir(), tt.args...)
			if err != nil {
				t.Fatalf("knobs: %#v", err)
			}
			for _, want := range tt.wants {
				if !strings.Contains(out, want) {
					t.Errorf("output has no %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestKnobsNoMatch(t *testing.T) {
	out, err := run(t, t.TempDir(), "knobs", "history.cachTTL")
	if err != nil {
		t.Fatalf("knobs: err = %#v, want exit 0", err)
	}
	if want := "no setting matches \"history.cachTTL\"\n  closest: history.cacheTTL\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestKnobsJSON(t *testing.T) {
	out, err := run(t, t.TempDir(), "knobs", "--json")
	if err != nil {
		t.Fatalf("knobs: %#v", err)
	}

	var doc knobsDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v", err)
	}
	if doc.Kind != "Knobs" || doc.Temporal != "1.31.3" || len(doc.Settings) < 500 {
		t.Errorf("kind %s, temporal %s, %d settings", doc.Kind, doc.Temporal, len(doc.Settings))
	}
	for _, k := range doc.Settings {
		if k.Apply != "unknown" {
			t.Errorf("%s: apply = %q, want unknown until it is classified", k.Key, k.Apply)
		}
	}
}

func TestKnobsRefusesEnv(t *testing.T) {
	_, err := run(t, t.TempDir(), "-e", "lab", "knobs")

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "usage.invalid" {
		t.Errorf("err = %#v, want a usage.invalid UserError", err)
	}
}

func TestDefaultOf(t *testing.T) {
	tests := []struct {
		name string
		s    catalog.Setting
		want string
	}{
		{"constant", catalog.Setting{Default: json.Number("1048576000"), DefaultExpr: "256000 * 4 * 1024"}, "1048576000"},
		{"bool", catalog.Setting{Default: false}, "false"},
		{"constrained", catalog.Setting{Default: []any{"5m", "1m"}}, "5m,1m"},
		{"not constant", catalog.Setting{DefaultExpr: "DefaultDynamicRateLimitingParams"}, "DefaultDynamicRateLimitingParams"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultOf(tt.s); got != tt.want {
				t.Errorf("defaultOf = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstSentence(t *testing.T) {
	tests := []struct{ in, want string }{
		{"HistoryCacheTTL is TTL of history cache. Requires service restart to take effect.", "HistoryCacheTTL is TTL of history cache"},
		{"one line\nsecond line", "one line"},
		{"no period", "no period"},
		{"ends with one.", "ends with one"},
		{"v1.2 is kept. Rest", "v1.2 is kept"},
	}
	for _, tt := range tests {
		if got := firstSentence(tt.in); got != tt.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
