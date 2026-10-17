package spec

import (
	"os"
	"testing"
)

// labSpec returns the lab spec from testdata, parsed and defaulted.
func labSpec(t *testing.T) *Cluster {
	t.Helper()
	data, err := os.ReadFile("testdata/lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %#v", err)
	}
	Defaults(c)
	return c
}

func TestValidateLab(t *testing.T) {
	if ps := Validate(labSpec(t)); len(ps) != 0 {
		t.Errorf("Validate(lab) = %v, want no problems", ps)
	}
}

func TestValidateProblems(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(c *Cluster)
		want   []string
	}{
		{"bad cluster name", func(c *Cluster) { c.Cluster = "Lab" }, []string{`cluster: "Lab" must match ^[a-z][a-z0-9-]{1,30}$`}},
		{"zero shards", func(c *Cluster) { c.Shards = 0 }, []string{"shards: 0 must be between 1 and 16384"}},
		{"no nodes", func(c *Cluster) { c.Nodes = nil }, []string{"nodes: at least one node is required"}},
		{"duplicate address", func(c *Cluster) { c.Nodes[1].Address = "10.78.0.11" }, []string{"nodes[1].address: 10.78.0.11 is also nodes[0]"}},
		{"duplicate name", func(c *Cluster) { c.Nodes[1].Name = "tscale-01" }, []string{"nodes[1].name: tscale-01 is also nodes[0]"}},
		{"bad node name", func(c *Cluster) { c.Nodes[0].Name = "Node_1" }, []string{`nodes[0].name: "Node_1" must match ^[a-z0-9][a-z0-9-]{0,62}$`}},
		{"not IPv4", func(c *Cluster) { c.Nodes[0].Address = "tscale-01.lan" }, []string{`nodes[0].address: "tscale-01.lan" is not an IPv4 address`}},
		{"every problem at once", func(c *Cluster) { c.Shards = 0; c.Nodes[1].Address = "10.78.0.11" }, []string{"shards: 0 must be between 1 and 16384", "nodes[1].address: 10.78.0.11 is also nodes[0]"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := labSpec(t)
			tt.mutate(c)

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
