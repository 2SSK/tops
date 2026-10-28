package spec

import (
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The entries of a strict-decode yaml.TypeError. decode_test.go pins this format.
var (
	entryLine    = regexp.MustCompile(`^line (\d+): (.*)$`)
	unknownField = regexp.MustCompile(`^field (\S+) not found in type \S+$`)
	wrongType    = regexp.MustCompile("^cannot unmarshal !!(\\w+)(?: `(.*)`)? into (\\S+)$")
)

// decodeProblems tursn every entry of a strict-decode error into a problem.
func decodeProblems(doc *yaml.Node, te *yaml.TypeError) []Problem {
	ps := make([]Problem, 0, len(te.Errors))
	for _, entry := range te.Errors {
		ps = append(ps, decodeProblem(doc, entry))
	}
	return ps
}

// decodeProblem turns "line 2: filed shard not found in tyep spec.Cluster" into
// {shard, 2, unknown key}. An entry in another format keeps its text.
func decodeProblem(doc *yaml.Node, entry string) Problem {
	m := entryLine.FindStringSubmatch(entry)
	if m == nil {
		return Problem{Message: entry}
	}
	line, _ := strconv.Atoi(m[1])

	if f := unknownField.FindStringSubmatch(m[2]); f != nil {
		return Problem{Path: pathOf(doc, keyAt(line, f[1])), Line: line, Message: "unknown key"}
	}
	if w := wrongType.FindStringSubmatch(m[2]); w != nil {
		return Problem{
			Path:    pathOf(doc, valueAt(line, w[1], w[2])),
			Line:    line,
			Message: "want " + want(w[3]) + ", got " + got(w[1], w[2]),
		}
	}

	return Problem{Line: line, Message: m[2]}
}

// want names a Go type the way the operator wrote YAML.
func want(goType string) string {
	switch {
	case goType == "int":
		return "a number"

	case goType == "bool":
		return "true or false"

	case goType == "string":
		return "a string"

	case strings.HasPrefix(goType, "[]"):
		return "a list"

	default:
		return "a mapping"
	}
}

// got names what the file has: a list, a mapping, or the value itself.
func got(tag, value string) string {
	switch tag {
	case "seq":
		return "a list"
	case "map":
		return "a mapping"
	default:
		return strconv.Quote(value)
	}
}

// match reports whether a key and its value are the node a problem is about.
// key is nil for sequence items and for the document itself.
type match func(key, value *yaml.Node) bool

// keyAt matches the key name on line.
func keyAt(line int, name string) match {
	return func(key, _ *yaml.Node) bool {
		return key != nil && key.Line == line && key.Value == name
	}
}

// valueAt matches the value on line that has the tag: a list, a mapping, or the scalar text.
func valueAt(line int, tag, text string) match {
	return func(_, value *yaml.Node) bool {
		if value.Line != line {
			return false
		}
		switch tag {
		case "seq":
			return value.Kind == yaml.SequenceNode
		case "map":
			return value.Kind == yaml.MappingNode
		default:
			return value.Kind == yaml.ScalarNode && value.Value == text
		}
	}
}

// pathOf returns the path of the last node in doc that m matches, like nodes[0].roles.
// The last match is the deepest: cluster: { name: a } has two mappings on line 1, the
// document and the value of cluster. With no match it returns "", the document.
func pathOf(doc *yaml.Node, m match) string {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	last := ""
	walk(doc, "", nil, func(path string, key, value *yaml.Node) {
		if m(key, value) {
			last = path
		}
	})

	return last
}

// walk calls visit for n and then for every value below it, in file order. key is the
// mapping key of a value, nil for sequence items and for the document.
func walk(n *yaml.Node, path string, key *yaml.Node, visit func(path string, key, value *yaml.Node)) {
	visit(path, key, n)
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			walk(n.Content[i+1], join(path, n.Content[i].Value), n.Content[i], visit)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			walk(item, path+"["+strconv.Itoa(i)+"]", nil, visit)
		}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
