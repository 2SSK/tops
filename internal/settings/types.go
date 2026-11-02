package settings

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/2SSK/tops/internal/catalog"
	"go.yaml.in/yaml/v3"
)

// tags are the YAML tags Temporal reads as each scalar type
// (common/dynamicconfig/collection.go, the convert functions).
var tags = map[string][]string{
	"bool":     {"!!bool"},
	"int":      {"!!int"},
	"float":    {"!!int", "!!float"},
	"string":   {"!!str"},
	"duration": {"!!int", "!!float"},
}

// decodes reports whether Temporal reads the YAML value n as a setting of type typ.
// A string can also be a bool ("true") or a duration ("15m").
func decodes(typ string, n *yaml.Node) bool {
	switch {
	case typ == "map":
		return n.Kind == yaml.MappingNode
	case typ == "typed":
		return true
	case slices.Contains(tags[typ], n.Tag):
		return true
	case n.Tag != "!!str":
		return false
	case typ == "bool":
		_, err := strconv.ParseBool(n.Value)
		return err == nil
	case typ == "duration":
		_, err := ParseDuration(n.Value)
		return err == nil
	}
	return false
}

var (
	unitless = regexp.MustCompile(`^(\d+(\.\d*)?|(\.\d+))$`)
	days     = regexp.MustCompile(`(\d+(\.\d*)?|(\.\d+))d`)
)

// ParseDuration reads a duration the way Temporal does (timestamp.ParseDurationDefaultSeconds):
// Go's syntax, plus d for 24 hours, and a number without a unit is seconds.
func ParseDuration(s string) (time.Duration, error) {
	if unitless.MatchString(s) {
		s += "s"
	}
	s = days.ReplaceAllStringFunc(s, func(d string) string {
		n, _ := strconv.ParseFloat(strings.TrimSuffix(d, "d"), 64)
		return strconv.FormatFloat(24*n, 'f', -1, 64) + "h"
	})
	return time.ParseDuration(s)
}

// want names what a setting's value must be.
func want(s catalog.Setting) string {
	switch s.Type {
	case "bool":
		return "true or false"
	case "int":
		return "a whole number"
	case "float":
		return "a number"
	case "string":
		return "a string"
	case "duration":
		return "a duration like 30s, 15m or 2d"
	default:
		return "a mapping"
	}
}

// got names what the file has: a list, a mapping, or the value itself.
func got(n *yaml.Node) string {
	switch n.Kind {
	case yaml.SequenceNode:
		return "a list"
	case yaml.MappingNode:
		return "a mapping"
	default:
		return strconv.Quote(n.Value)
	}
}
