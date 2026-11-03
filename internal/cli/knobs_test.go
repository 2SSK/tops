package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
		"\n  history.cacheTTL                           history  restart  1h       HistoryCacheTTL is TTL of history cache\n",
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
			"  apply       restart\n",
			"  default     1h\n",
			"\n  HistoryCacheTTL is TTL of history cache. Requires service restart to take effect.\n",
		}},
		{"verbose", []string{"knobs", "history.cacheTTL", "-v"}, []string{
			"  constraints  none\n",
			"  source       time.Hour\n",
			"  hint         restart (static, from the source; not a classification)\n",
			"  evidence     service/history/workflow/cache/cache.go NewHostLevelCache reads HistoryCacheTTL() once into the cache options\n",
		}},
		{"hot", []string{"knobs", "frontend.shutdownDrainDuration", "-v"}, []string{
			"  apply        hot\n",
			"  lab          pending\n",
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
	applies := map[string]string{}
	for _, k := range doc.Settings {
		applies[k.Key] = k.Apply
	}
	for key, want := range map[string]string{
		"history.cacheTTL":               "restart",
		"frontend.shutdownDrainDuration": "hot",
		"history.eventsCacheTTL":         "unknown",
	} {
		if applies[key] != want {
			t.Errorf("%s: apply = %q, want %q", key, applies[key], want)
		}
	}
}

func TestKnobsNoteOnlyForUnknown(t *testing.T) {
	out, err := run(t, t.TempDir(), "knobs", "shutdownDrainDuration")
	if err != nil {
		t.Fatalf("knobs: %#v", err)
	}
	if strings.Contains(out, "unknown =") {
		t.Errorf("every match is classified, but the output explains unknown:\n%s", out)
	}
}

func TestKnobsLayers(t *testing.T) {
	tests := []struct {
		key   string
		wants []string
	}{
		{"frontend.namespaceRPS", []string{
			"layers in lab\n\n",
			"  default                          2400\n",
			"  base/common.yaml                 -\n",
			"  base/frontend.yaml               -\n",
			"  environments/lab/overrides.yaml  400 (namespace=drills), 2400\n",
			"  frontend runs                    400 (namespace=drills), 2400  from environments/lab/overrides.yaml\n",
		}},
		{"limit.maxIDLength", []string{
			"  base/common.yaml                 1000\n",
			"  frontend runs                    1000  from base/common.yaml\n",
			"  worker runs                      1000  from base/common.yaml\n",
		}},
		{"history.eventsCacheTTL", []string{
			"  history runs                     1h  from default\n",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			out, err := run(t, fixtureFleet(t), "-e", "lab", "knobs", tt.key)
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

func TestKnobsLayersJSON(t *testing.T) {
	out, err := run(t, fixtureFleet(t), "-e", "lab", "knobs", "frontend.namespaceRPS", "--json")
	if err != nil {
		t.Fatalf("knobs: %#v", err)
	}

	var doc knobsDocument
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v", err)
	}
	if len(doc.Settings) != 1 || doc.Settings[0].Layers == nil {
		t.Fatalf("settings = %+v, want one with layers", doc.Settings)
	}
	if got := doc.Settings[0].Layers.Effective["frontend"].File; got != "environments/lab/overrides.yaml" {
		t.Errorf("frontend runs the list from %q, want the overrides", got)
	}
	if !strings.Contains(out, `{"value":400,"constraints":{"namespace":"drills"}}`) {
		t.Errorf("constraints must be in lower case, like value: %s", out)
	}
}

func TestKnobsNoLayersWithoutEnv(t *testing.T) {
	out, err := run(t, t.TempDir(), "knobs", "frontend.namespaceRPS", "--json")
	if err != nil {
		t.Fatalf("knobs: %#v", err)
	}
	if strings.Contains(out, `"layers"`) {
		t.Errorf("without -e there is no environment, so no layers: %s", out)
	}
}

func TestKnobsInvalidSettings(t *testing.T) {
	root := fixtureFleet(t)
	if err := os.WriteFile(filepath.Join(root, "base", "history.yaml"), []byte("frontend.namespaceRPS:\n  - value: 400\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, root, "-e", "lab", "knobs", "frontend.namespaceRPS")

	var ue *term.UserError
	if !errors.As(err, &ue) || ue.Code != "spec.invalid" || ue.What != "environments/lab has invalid settings" {
		t.Errorf("err = %#v, want spec.invalid naming the environment", err)
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
