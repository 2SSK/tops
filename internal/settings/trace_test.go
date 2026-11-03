package settings

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestTrace(t *testing.T) {
	l := layers(t,
		"limit.maxIDLength:\n  - value: 500\n",
		"limit.maxIDLength:\n  - value: 300\n",
		"frontend.namespaceRPS:\n  - value: 400\n    constraints: { namespace: drills }\n",
	)
	c := load(t)

	tests := []struct {
		key  string
		want Trace
	}{
		{"frontend.namespaceRPS", Trace{
			Default:  json.Number("2400"), // the catalog keeps numbers as written
			Override: []Value{{Value: 400, Constraints: map[string]any{"namespace": "drills"}}},
			Effective: map[string]Source{
				"frontend": {File: "environments/lab/overrides.yaml", Values: []Value{{Value: 400, Constraints: map[string]any{"namespace": "drills"}}}},
			},
		}},
		{"limit.maxIDLength", Trace{
			Default: json.Number("1000"),
			Common:  []Value{{Value: 500}},
			Roles:   map[string][]Value{"history": {{Value: 300}}},
			Effective: map[string]Source{
				"frontend": {File: "base/common.yaml", Values: []Value{{Value: 500}}},
				"history":  {File: "base/history.yaml", Values: []Value{{Value: 300}}},
				"matching": {File: "base/common.yaml", Values: []Value{{Value: 500}}},
				"worker":   {File: "base/common.yaml", Values: []Value{{Value: 500}}},
			},
		}},
		{"history.cacheTTL", Trace{
			Default:   "1h",
			Effective: map[string]Source{"history": {File: "default", Values: []Value{{Value: "1h"}}}},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			s, _ := c.Lookup(tt.key)
			if got := l.Trace(s, c); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Trace =\n%#v\nwant\n%#v", got, tt.want)
			}
		})
	}
}

func TestReaders(t *testing.T) {
	c := load(t)
	for key, want := range map[string][]string{
		"history.cacheTTL":  {"history"},
		"limit.maxIDLength": {"frontend", "history", "matching", "worker"},
	} {
		s, _ := c.Lookup(key)
		if got := Readers(s); !slices.Equal(got, want) {
			t.Errorf("Readers(%s) = %v, want %v", key, got, want)
		}
	}
}
