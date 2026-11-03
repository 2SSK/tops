package settings

import (
	"slices"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/spec"
)

// Trace is where a setting gets its value in an environment (backend schema Knobs
// layers): the catalog default, the list each file sets, and what each role that reads
// it runs. A role file is in Roles only when it sets the key.
type Trace struct {
	Default   any                `json:"default"`
	Common    []Value            `json:"common,omitempty"`
	Roles     map[string][]Value `json:"roles,omitempty"`
	Override  []Value            `json:"override,omitempty"`
	Effective map[string]Source  `json:"effective"`
}

// Source is the list a role runs and the file it comes from, "default" when no file sets it.
type Source struct {
	File   string  `json:"file"`
	Values []Value `json:"values"`
}

// Trace is where s gets its value, for every role that reads it.
func (l Layers) Trace(s catalog.Setting, c *catalog.Catalog) Trace {
	t := Trace{
		Default:   s.Default,
		Common:    l.Common[s.Key].Values,
		Override:  l.Overrides[s.Key].Values,
		Effective: map[string]Source{},
	}
	for _, role := range Readers(s) {
		if e, ok := l.Roles[role][s.Key]; ok {
			if t.Roles == nil {
				t.Roles = map[string][]Value{}
			}
			t.Roles[role] = e.Values
		}
		source := Source{File: "default", Values: []Value{{Value: s.Default}}}
		if e, ok := l.Merge(role, c)[s.Key]; ok {
			source = Source{File: e.File, Values: e.Values}
		}
		t.Effective[role] = source
	}
	return t
}

// Readers are the roles that read s, in spec.Roles order: shared means every role.
func Readers(s catalog.Setting) []string {
	return slices.DeleteFunc(slices.Clone(spec.Roles), func(role string) bool { return !reads(s, role) })
}
