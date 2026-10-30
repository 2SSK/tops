// Package catalog is the settings catalog of each supported Temporal minor, embedded in
// the binary: every dynamic-config key with its type, default, roles and apply hint.
package catalog

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

//go:embed catalog-*.json
var files embed.FS

// Catalog is one catalog-<minor>.json, written by tools/cataloggen.
type Catalog struct {
	Temporal      string    `json:"temporal"`
	GeneratedFrom string    `json:"generatedFrom"`
	Settings      []Setting `json:"settings"`
}

// Setting is one dynamic-config key (backend schema "Settings catalog").
type Setting struct {
	Key         string   `json:"key"`
	Type        string   `json:"type"`
	Precedence  string   `json:"precedence"`
	Constraints []string `json:"constraints"`
	Default     any      `json:"default"`
	DefaultExpr string   `json:"defaultExpr"`
	Description string   `json:"description"`
	Roles       []string `json:"roles"`
	Candidate   string   `json:"candidate"`
}

// Minors are the Temporal minors with an embedded catalog, oldest first.
func Minors() []string {
	names, _ := fs.Glob(files, "catalog-*.json") // the pattern is valid, so no error
	minors := make([]string, 0, len(names))
	for _, n := range names {
		minors = append(minors, strings.TrimSuffix(strings.TrimPrefix(n, "catalog-"), ".json"))
	}
	slices.SortFunc(minors, compareMinors)
	return minors
}

// Load reads the catalog of minor, such as 1.31.
func Load(minor string) (*Catalog, error) {
	data, err := files.ReadFile("catalog-" + minor + ".json")
	if err != nil {
		return nil, fmt.Errorf("no settings catalog for Temporal %s", minor)
	}
	// Numbers stay json.Number: a float64 would print 1048576000 as 1.048576e+09.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var c Catalog
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("catalog-%s.json: %w", minor, err)
	}
	return &c, nil
}

// Lookup is the setting named key; Temporal matches keys case-insensitively.
func (c *Catalog) Lookup(key string) (Setting, bool) {
	for _, s := range c.Settings {
		if strings.EqualFold(s.Key, key) {
			return s, true
		}
	}
	return Setting{}, false
}

// Search is every setting whose key contains text, then every other one whose
// description does, ignoring case; an empty text matches all.
func (c *Catalog) Search(text string) []Setting {
	text = strings.ToLower(text)
	var byKey, byDescription []Setting
	for _, s := range c.Settings {
		switch {
		case strings.Contains(strings.ToLower(s.Key), text):
			byKey = append(byKey, s)
		case strings.Contains(strings.ToLower(s.Description), text):
			byDescription = append(byDescription, s)
		}

	}
	return append(byKey, byDescription...)
}

// Closest are up to three known keys for a key that is not one: those it is a prefix
// of, else those at the smallest edit distance.
func (c *Catalog) Closest(key string) []string {
	key = strings.ToLower(key)
	var prefixed, nearest []string
	best := -1
	for _, s := range c.Settings {
		k := strings.ToLower(s.Key)
		if strings.HasPrefix(k, key) {
			prefixed = append(prefixed, s.Key)
		}
		switch d := distance(key, k); {
		case best < 0 || d < best:
			best, nearest = d, []string{s.Key}
		case d == best:
			nearest = append(nearest, s.Key)
		}
	}
	if len(prefixed) == 0 {
		prefixed = nearest
	}
	return prefixed[:min(3, len(prefixed))]
}

// distance is the Levenshtein distance: the fewest single-byte inserts, deletes and
// substitutions that turn a into b.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// compareMinors orders minors numerically: 1.9 before 1.10.
func compareMinors(a, b string) int {
	am, an := splitMinor(a)
	bm, bn := splitMinor(b)
	if am != bm {
		return am - bm
	}
	return an - bn
}

func splitMinor(v string) (major, minor int) {
	ma, mi, _ := strings.Cut(v, ".")
	major, _ = strconv.Atoi(ma)
	minor, _ = strconv.Atoi(mi)
	return major, minor
}
