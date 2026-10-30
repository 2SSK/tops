package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func load(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load("1.31")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMinors(t *testing.T) {
	if got := Minors(); !slices.Equal(got, []string{"1.31"}) {
		t.Errorf("Minors() = %v, want [1.31]", got)
	}
}

func TestLoad(t *testing.T) {
	c := load(t)
	if c.Temporal != "1.31.3" || len(c.Settings) < 500 {
		t.Errorf("temporal = %q with %d settings, want 1.31.3 with more than 500", c.Temporal, len(c.Settings))
	}

	s, ok := c.Lookup("history.cacheTTL")
	if !ok {
		t.Fatal("history.cacheTTL is missing")
	}
	if s.Default != "1h" || !slices.Equal(s.Roles, []string{"history"}) || s.Candidate != "restart" {
		t.Errorf("history.cacheTTL = default %v, roles %v, candidate %q; want 1h, [history], restart", s.Default, s.Roles, s.Candidate)
	}

	s, _ = c.Lookup("history.hostLevelCacheMaxSizeBytes")
	if s.Default != json.Number("1048576000") {
		t.Errorf("history.hostLevelCacheMaxSizeBytes default = %#v, want json.Number 1048576000", s.Default)
	}

	if _, err := Load("1.29"); err == nil || err.Error() != "no settings catalog for Temporal 1.29" {
		t.Errorf("Load(1.29) err = %v", err)
	}
}

func TestLookup(t *testing.T) {
	c := load(t)
	if s, ok := c.Lookup("HISTORY.CACHETTL"); !ok || s.Key != "history.cacheTTL" {
		t.Errorf("Lookup ignores case: got %q, %v", s.Key, ok)
	}
	if _, ok := c.Lookup("history.cache"); ok {
		t.Errorf("Lookup(history.cache) matched a prefix")
	}
}

func TestSearch(t *testing.T) {
	c := load(t)
	if got := len(c.Search("")); got != len(c.Settings) {
		t.Errorf("Search(\"\") = %d settings, want all %d", got, len(c.Settings))
	}

	got := c.Search("cache")
	keyMatch := func(s Setting) bool { return containsFold(s.Key, "cache") }
	i := slices.IndexFunc(got, func(s Setting) bool { return !keyMatch(s) })
	if i <= 0 {
		t.Fatalf("Search(cache): key matches end at %d, want key matches then description matches", i)
	}
	if slices.ContainsFunc(got[i:], keyMatch) {
		t.Errorf("Search(cache): a key match come after a description match")
	}

	if got := c.Search("no setting says this"); len(got) != 0 {
		t.Errorf("Search(nonsense) = %d settings", len(got))
	}
}

func TestClosest(t *testing.T) {
	c := load(t)
	tests := []struct {
		key  string
		want []string
	}{
		{"history.cacheTT", []string{"history.cacheTTL"}},
		{"history.cachTTL", []string{"history.cacheTTL"}},
		{"HISTORY.CACHETTL", []string{"history.cacheTTL"}},
	}

	for _, tt := range tests {
		if got := c.Closest(tt.key); !slices.Equal(got, tt.want) {
			t.Errorf("Closest(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}

	if got := c.Closest("history."); len(got) != 3 {
		t.Errorf("Closest(history.) = %v, want 3 keys", got)
	}
}

func TestDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"cacheTTL", "cacheTTL", 0},
		{"cachTTL", "cacheTTL", 1},
	}

	for _, tt := range tests {
		if got := distance(tt.a, tt.b); got != tt.want {
			t.Errorf("distance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCompareMinors(t *testing.T) {
	got := []string{"1.31", "1.9", "1.30", "2.0", "1.10"}
	slices.SortFunc(got, compareMinors)
	if want := []string{"1.9", "1.10", "1.30", "1.31", "2.0"}; !slices.Equal(got, want) {
		t.Errorf("sorted = %v, want %v", got, want)
	}
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
