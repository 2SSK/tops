package spec

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// lineOf returns the line of path in doc: nodes[2].roles[1] is the line of the second
// role of the third node. When part of the path is missing, it returns the line of the
// deepest part that exists, so a missing required key points at its parent.
func lineOf(doc *yaml.Node, path string) int {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}

	line := n.Line
	for _, step := range steps(path) {
		next, at := child(n, step)
		if next == nil {
			break
		}
		n, line = next, at
	}

	return line
}

// steps splits nodes[2].roles[1] into nodes, [2], roles, [1].
func steps(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(path, "[", ".["), ".")
}

// child returns the node that step selects in n and the line to report for it: the key's
// line in a mapping, the item's line in a sequence. It returns nil when there is none.
func child(n *yaml.Node, step string) (*yaml.Node, int) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Value == step {
				return n.Content[i+1], key.Line
			}
		}
	case yaml.SequenceNode:
		i, err := strconv.Atoi(strings.Trim(step, "[]"))
		if err == nil && i >= 0 && i < len(n.Content) {
			return n.Content[i], n.Content[i].Line
		}
	}

	return nil, 0
}
