package spec

import (
	"strings"
	"testing"
)

func TestParseSize(t *testing.T) {
	good := map[string]int64{
		"512M":  512 << 20,
		"2G":    2 << 30,
		"1K":    1 << 10,
		"1T":    1 << 40,
		"1536M": 1536 << 20,
	}
	for s, want := range good {
		if got, err := ParseSize(s); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", s, got, err, want)
		}
	}
	for _, s := range []string{
		"", "M", "512", // no unit: 512 is almost always a typo for 512M
		"0M", "-1G", "1.5G", "512MB", "512m", "512 M", " 512M",
		"9999999T", // overflows int64 bytes
	} {
		if got, err := ParseSize(s); err == nil {
			t.Errorf("ParseSize(%q) = %d, want an error", s, got)
		} else if !strings.Contains(err.Error(), "is not a size like 512M or 2G") {
			t.Errorf("ParseSize(%q): %v", s, err)
		}
	}
}

func ptr(n int) *int { return &n }

func TestValidatePins(t *testing.T) {
	tests := []struct {
		name string
		pin  map[string]Pin // on vps-03, which runs frontend and matching
		want []string
	}{
		{"the lab's pin", map[string]Pin{"matching": {MemoryMax: "512M"}}, nil},
		{"every field", map[string]Pin{"frontend": {MemoryMax: "1G", CPUWeight: ptr(200), DBPool: ptr(10), DBVisibilityPool: ptr(2)}}, nil},
		{"weight bounds", map[string]Pin{"frontend": {CPUWeight: ptr(1)}, "matching": {CPUWeight: ptr(10000)}}, nil},
		{"a role the node does not run", map[string]Pin{"history": {MemoryMax: "1G"}},
			[]string{"nodes[2].pin.history: history is not one of this node's roles (frontend, matching)"}},
		{"not a role at all", map[string]Pin{"ui": {MemoryMax: "256M"}},
			[]string{"nodes[2].pin.ui: ui is not one of this node's roles (frontend, matching)"}},
		{"pins nothing: the formula stays", map[string]Pin{"matching": {}}, nil},
		{"size without a unit", map[string]Pin{"matching": {MemoryMax: "512"}},
			[]string{`nodes[2].pin.matching.memoryMax: "512" is not a size like 512M or 2G`}},
		{"weight zero", map[string]Pin{"matching": {CPUWeight: ptr(0)}},
			[]string{"nodes[2].pin.matching.cpuWeight: 0 must be between 1 and 10000"}},
		{"weight too high", map[string]Pin{"matching": {CPUWeight: ptr(10001)}},
			[]string{"nodes[2].pin.matching.cpuWeight: 10001 must be between 1 and 10000"}},
		{"empty pools", map[string]Pin{"matching": {DBPool: ptr(0), DBVisibilityPool: ptr(-1)}},
			[]string{"nodes[2].pin.matching.dbPool: 0 must be at least 1", "nodes[2].pin.matching.dbVisibilityPool: -1 must be at least 1"}},
		{"in role order, every problem", map[string]Pin{"matching": {CPUWeight: ptr(0)}, "frontend": {MemoryMax: "big"}},
			[]string{`nodes[2].pin.frontend.memoryMax: "big" is not a size like 512M or 2G`, "nodes[2].pin.matching.cpuWeight: 0 must be between 1 and 10000"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := labSpec(t)
			c.Nodes[2].Pin = tt.pin
			got := Validate(c)
			if len(got) != len(tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i].String() != tt.want[i] {
					t.Errorf("problem %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestPinFromYAML: pins decode from the spec, a wrong type or key is a decode problem on
// its own line, and a validation problem points at the pinned key's line.
func TestPinFromYAML(t *testing.T) {
	lab := labSpec(t)
	if got := lab.Nodes[2].Pin["matching"].MemoryMax; got != "512M" {
		t.Errorf("lab vps-03 pin = %q, want matching memoryMax 512M", got)
	}

	spec := func(pin string) string {
		return `cluster: lab
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512
persistence:
  postgres: { host: 10.80.0.20, user: temporal }
ssh: { user: deploy }
namespaces: [{ name: default, retention: 72h }]
nodes:
  - name: vps-01
    address: 10.80.0.11
    roles: [frontend, history, matching, worker]
    pin:
` + pin
	}
	tests := []struct {
		name, pin string
		want      Problem
	}{
		{"weight as text", "      matching:\n        cpuWeight: lots\n",
			Problem{Path: "nodes[0].pin.matching.cpuWeight", Line: 14, Message: `want a number, got "lots"`}},
		{"unknown key", "      matching:\n        memory: 512M\n",
			Problem{Path: "nodes[0].pin.matching.memory", Line: 14, Message: "unknown key"}},
		{"bad size", "      history:\n        memoryMax: 2GB\n",
			Problem{Path: "nodes[0].pin.history.memoryMax", Line: 14, Message: `"2GB" is not a size like 512M or 2G`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse("cluster.yaml", []byte(spec(tt.pin)))
			if err != nil {
				t.Fatalf("Parse: %#v", err)
			}
			Defaults(c)
			got := Validate(c)
			if len(got) != 1 {
				t.Fatalf("Validate() = %+v, want one problem", got)
			}
			if got[0].Path != tt.want.Path || got[0].Line != tt.want.Line || got[0].Message != tt.want.Message {
				t.Errorf("problem = %+v, want %+v", got[0], tt.want)
			}
		})
	}
}

// TestPinsWaitForRoles: with the node's roles wrong, its pins are not checked against them,
// so one mistake is one problem.
func TestPinsWaitForRoles(t *testing.T) {
	c := labSpec(t)
	c.Nodes[2].Roles[1] = "matchng"
	c.Nodes[2].Pin = map[string]Pin{"matching": {CPUWeight: ptr(0)}}
	got := Validate(c)
	if len(got) != 1 || got[0].Path != "nodes[2].roles[1]" {
		t.Errorf("Validate() = %v, want only the role problem", got)
	}
}

// TestEveryPinFieldFromYAML: each field decodes from the key the backend schema documents.
// A tag that differs, even only in case, makes the key "unknown" and the field never set.
func TestEveryPinFieldFromYAML(t *testing.T) {
	c, err := Parse("cluster.yaml", []byte(`cluster: lab
temporal: { version: 1.31.3, cli: 1.9.1 }
shards: 512
persistence:
  postgres: { host: 10.80.0.20, user: temporal }
ssh: { user: deploy }
namespaces: [{ name: default, retention: 72h }]
nodes:
  - name: vps-01
    address: 10.80.0.11
    roles: [frontend, history, matching, worker]
    pin:
      history: { memoryMax: 2G, cpuWeight: 150, dbPool: 20, dbVisibilityPool: 4 }
`))
	if err != nil {
		t.Fatalf("Parse: %#v", err)
	}
	Defaults(c)
	if ps := Validate(c); len(ps) != 0 {
		t.Fatalf("Validate() = %v, want no problems", ps)
	}
	p := c.Nodes[0].Pin["history"]
	if p.MemoryMax != "2G" || p.CPUWeight == nil || *p.CPUWeight != 150 || p.DBPool == nil || *p.DBPool != 20 ||
		p.DBVisibilityPool == nil || *p.DBVisibilityPool != 4 {
		t.Errorf("pin = %+v, want every field set", p)
	}
}
