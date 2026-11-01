package settings

import (
	"reflect"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/spec"
)

func load(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load("1.31")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParse(t *testing.T) {
	data := `# overrides
History.CacheTTL:
  - value: 15m
frontend.namespaceRPS:
  - value: 400
    constraints: { namespace: drills }
  - value: 2400
`
	f, problems, err := Parse("overrides.yaml", []byte(data), load(t))
	if err != nil || len(problems) > 0 {
		t.Fatalf("err %v, problems %v", err, problems)
	}
	want := File{
		"history.cacheTTL": {Key: "history.cacheTTL", File: "overrides.yaml", Line: 2, Values: []Value{{Value: "15m"}}},
		"frontend.namespaceRPS": {Key: "frontend.namespaceRPS", File: "overrides.yaml", Line: 4, Values: []Value{
			{Value: 400, Constraints: map[string]any{"namespace": "drills"}},
			{Value: 2400},
		}},
	}
	if !reflect.DeepEqual(f, want) {
		t.Errorf("Parse =\n%#v\nwant\n%#v", f, want)
	}
}

func TestParseEmpty(t *testing.T) {
	for _, data := range []string{"", "# nothing here yet\n"} {
		f, problems, err := Parse("base/common.yaml", []byte(data), load(t))
		if err != nil || len(problems) > 0 || len(f) > 0 {
			t.Errorf("%q: file %v, problems %v, err %v; want nothing", data, f, problems, err)
		}
	}
}

func TestParseNotYAML(t *testing.T) {
	if _, _, err := Parse("base/common.yaml", []byte("a: [\n"), load(t)); err == nil {
		t.Error("err = nil, want the YAML error")
	}
}

func TestParseProblems(t *testing.T) {
	tests := []struct {
		name, data string
		want       spec.Problem
	}{
		{"not a mapping", "- a\n", spec.Problem{Line: 1, Message: "want a mapping of keys to lists of values"}},
		{"unknown key", "history.cachTTL:\n  - value: 1m\n", spec.Problem{Path: "history.cachTTL", Line: 1, Message: "not a setting of Temporal 1.31.3; did you mean history.cacheTTL?"}},
		{"same key twice", "history.cacheTTL:\n  - value: 1m\nHistory.CacheTTL:\n  - value: 2m\n", spec.Problem{Path: "History.CacheTTL", Line: 3, Message: "history.cacheTTL is also set on line 1"}},
		{"not a list", "history.cacheTTL: 15m\n", spec.Problem{Path: "history.cacheTTL", Line: 1, Message: "want a list of values: - value: ..."}},
		{"item not a mapping", "history.cacheTTL:\n  - 15m\n", spec.Problem{Path: "history.cacheTTL[0]", Line: 2, Message: "want value: and optional constraints:"}},
		{"unknown field", "frontend.namespaceRPS:\n  - value: 1\n    constraint: { namespace: a }\n", spec.Problem{Path: "frontend.namespaceRPS[0].constraint", Line: 3, Message: "unknown key (value, constraints)"}},
		{"value twice", "history.cacheTTL:\n  - value: 1m\n    value: 2m\n", spec.Problem{Path: "history.cacheTTL[0].value", Line: 3, Message: "value is also on line 2"}},
		{"no value", "frontend.namespaceRPS:\n  - constraints: { namespace: a }\n", spec.Problem{Path: "frontend.namespaceRPS[0].value", Line: 2, Message: "value is required"}},
		{"wrong duration", "history.cacheTTL:\n  - value: 15 minutes\n", spec.Problem{Path: "history.cacheTTL[0].value", Line: 2, Message: `want a duration like 30s, 15m or 2d, got "15 minutes"`}},
		{"float for int", "limit.maxIDLength:\n  - value: 1.5\n", spec.Problem{Path: "limit.maxIDLength[0].value", Line: 2, Message: `want a whole number, got "1.5"`}},
		{"list for bool", "history.enableHostLevelEventsCache:\n  - value: [true]\n", spec.Problem{Path: "history.enableHostLevelEventsCache[0].value", Line: 2, Message: "want true or false, got a list"}},
		{"constraint on global", "history.cacheTTL:\n  - value: 1m\n    constraints: { namespace: a }\n", spec.Problem{Path: "history.cacheTTL[0].constraints.namespace", Line: 3, Message: "history.cacheTTL takes no constraints (precedence global)"}},
		{"wrong constraint", "frontend.namespaceRPS:\n  - value: 1\n    constraints: { taskQueueName: q }\n", spec.Problem{Path: "frontend.namespaceRPS[0].constraints.taskQueueName", Line: 3, Message: "frontend.namespaceRPS takes only namespace (precedence namespace)"}},
		{"constraints not a mapping", "frontend.namespaceRPS:\n  - value: 1\n    constraints: a\n", spec.Problem{Path: "frontend.namespaceRPS[0].constraints", Line: 3, Message: "want a mapping, like { namespace: default }"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, problems, err := Parse("overrides.yaml", []byte(tt.data), load(t))
			if err != nil {
				t.Fatal(err)
			}
			tt.want.File = "overrides.yaml"
			if len(problems) != 1 || problems[0] != tt.want {
				t.Errorf("problems = %+v, want [%+v]", problems, tt.want)
			}
			if tt.name != "same key twice" && len(f) > 0 {
				t.Errorf("a key with a problem is in the file: %v", f)
			}
		})
	}
}

func TestDecodes(t *testing.T) {
	tests := []struct {
		typ, value string
		want       bool
	}{
		{"bool", "true", true},
		{"bool", `"false"`, true},
		{"bool", "yes", false},
		{"int", "400", true},
		{"int", `"400"`, false},
		{"float", "400", true},
		{"float", "0.5", true},
		{"string", "drills", true},
		{"string", "4", false},
		{"duration", "15m", true},
		{"duration", "30", true},
		{"duration", "2d", true},
		{"duration", "soon", false},
		{"map", "{ a: 1 }", true},
		{"map", "a", false},
		{"typed", "[1, 2]", true},
	}
	for _, tt := range tests {
		var n yaml.Node
		if err := yaml.Unmarshal([]byte(tt.value), &n); err != nil {
			t.Fatal(err)
		}
		if got := decodes(tt.typ, n.Content[0]); got != tt.want {
			t.Errorf("decodes(%s, %s) = %v, want %v", tt.typ, tt.value, got, tt.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"15m", 15 * time.Minute},
		{"30", 30 * time.Second},
		{"1.5", 1500 * time.Millisecond},
		{"2d", 48 * time.Hour},
		{"1d12h", 36 * time.Hour},
		{"0.5d", 12 * time.Hour},
	}
	for _, tt := range tests {
		if got, err := ParseDuration(tt.in); err != nil || got != tt.want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
	if _, err := ParseDuration("15 minutes"); err == nil {
		t.Error("ParseDuration(15 minutes) err = nil, want one")
	}
}
