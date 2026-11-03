// Package settings reads the fleet's dynamic-config files (base/ and overrides.yaml),
// checks them against the settings catalog, and merges them per role.
package settings

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

// File is one dynamic-config file: its entries by the catalog's spelling of each key.
type File map[string]Entry

// Entry is one key of one file: the values written for it, and where.
type Entry struct {
	Key    string // the catalog's spelling
	File   string
	Line   int
	Values []Value
}

// Value is one item of a key's list: the value, and the constraints it applies under
// (none for the fallback).
type Value struct {
	Value       any            `yaml:"value" json:"value"`
	Constraints map[string]any `yaml:"constraints,omitempty" json:"constraints,omitempty"`
}

// Parse reads the dynamic-config file named file. YAML that does not parse is an error;
// every other problem is returned, and a key with a problem is left out of the File.
func Parse(file string, data []byte, c *catalog.Catalog) (File, []spec.Problem, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, &term.UserError{Code: "spec.invalid", What: file + " is invalid", Why: err.Error(), Fix: "edit " + file}
	}
	p := &parser{file: file, c: c, f: File{}}
	if len(doc.Content) == 0 {
		return p.f, nil, nil // an empty file sets nothing
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		p.addf("", root.Line, "want a mapping of keys to lists of values")
		return p.f, p.problems, nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		p.entry(root.Content[i], root.Content[i+1])
	}
	return p.f, p.problems, nil
}

type parser struct {
	file     string
	c        *catalog.Catalog
	f        File
	problems []spec.Problem
}

func (p *parser) addf(path string, line int, format string, args ...any) {
	p.problems = append(p.problems, spec.Problem{File: p.file, Path: path, Line: line, Message: fmt.Sprintf(format, args...)})
}

// entry checks one key and its list, and adds it to the File if both are right.
func (p *parser) entry(key, list *yaml.Node) {
	s, ok := p.c.Lookup(key.Value)
	if !ok {
		p.addf(key.Value, key.Line, "not a setting of Temporal %s; did you mean %s?", p.c.Temporal, strings.Join(p.c.Closest(key.Value), ", "))
		return
	}
	if prev, dup := p.f[s.Key]; dup {
		p.addf(key.Value, key.Line, "%s is also set on line %d", s.Key, prev.Line)
		return
	}
	if list.Kind != yaml.SequenceNode || len(list.Content) == 0 {
		p.addf(key.Value, list.Line, "want a list of values: - value: ...")
		return
	}
	values := make([]Value, 0, len(list.Content))
	for i, item := range list.Content {
		v, ok := p.value(fmt.Sprintf("%s[%d]", key.Value, i), s, item)
		if !ok {
			return
		}
		values = append(values, v)
	}
	p.f[s.Key] = Entry{Key: s.Key, File: p.file, Line: key.Line, Values: values}
}

// value checks one item of a list: {value, constraints?}, the value of the setting's
// type, and only constraints its precedence allows.
func (p *parser) value(path string, s catalog.Setting, item *yaml.Node) (Value, bool) {
	value, constraints, ok := p.fields(path, item)
	if !ok {
		return Value{}, false
	}
	if !decodes(s.Type, value) {
		p.addf(path+".value", value.Line, "want %s, got %s", want(s), got(value))
		return Value{}, false
	}
	if constraints != nil && !p.constraints(path+".constraints", s, constraints) {
		return Value{}, false
	}
	var v Value
	// Both nodes came from the parsed file, so they decode into any.
	_ = value.Decode(&v.Value)
	if constraints != nil {
		_ = constraints.Decode(&v.Constraints)
	}
	return v, true
}

// fields splits an item into its value and constraints nodes. Temporal ignores any other
// key, so a typo like constraint: would apply the value everywhere: it is a problem, and
// so is a key written twice, which Temporal refuses.
func (p *parser) fields(path string, item *yaml.Node) (value, constraints *yaml.Node, ok bool) {
	if item.Kind != yaml.MappingNode {
		p.addf(path, item.Line, "want value: and optional constraints:")
		return nil, nil, false
	}
	ok = true
	seen := map[string]int{}
	for i := 0; i+1 < len(item.Content); i += 2 {
		k := item.Content[i]
		if line, dup := seen[k.Value]; dup {
			p.addf(path+"."+k.Value, k.Line, "%s is also on line %d", k.Value, line)
			ok = false
			continue
		}
		seen[k.Value] = k.Line
		switch k.Value {
		case "value":
			value = item.Content[i+1]
		case "constraints":
			constraints = item.Content[i+1]
		default:
			p.addf(path+"."+k.Value, k.Line, "unknown key (value, constraints)")
			ok = false
		}
	}
	if value == nil && ok {
		p.addf(path+".value", item.Line, "value is required")
		ok = false
	}
	return value, constraints, ok
}

// constraints checks that every constraint name is one the setting's precedence allows.
// Temporal compares the names ignoring case.
func (p *parser) constraints(path string, s catalog.Setting, m *yaml.Node) bool {
	if m.Kind != yaml.MappingNode {
		p.addf(path, m.Line, "want a mapping, like { namespace: default }")
		return false
	}
	ok := true
	for i := 0; i+1 < len(m.Content); i += 2 {
		name := m.Content[i]
		if !containsFold(s.Constraints, name.Value) {
			p.addf(path+"."+name.Value, name.Line, "%s", notAllowed(s))
			ok = false
		}
	}
	return ok
}

// notAllowed explains which constraints a setting takes.
func notAllowed(s catalog.Setting) string {
	if len(s.Constraints) == 0 {
		return fmt.Sprintf("%s takes no constraints (precedence %s)", s.Key, s.Precedence)
	}
	return fmt.Sprintf("%s takes only %s (precedence %s)", s.Key, strings.Join(s.Constraints, ", "), s.Precedence)
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
