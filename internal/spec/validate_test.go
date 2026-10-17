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
		{"no nodes", func(c *Cluster) { c.Nodes = nil }, []string{"nodes: at least one node is required", `ui.node: "tscale-01" is not a name in nodes`}},
		{"duplicate address", func(c *Cluster) { c.Nodes[1].Address = "10.78.0.11" }, []string{"nodes[1].address: 10.78.0.11 is also nodes[0]"}},
		{"duplicate name", func(c *Cluster) { c.Nodes[1].Name = "tscale-01" }, []string{"nodes[1].name: tscale-01 is also nodes[0]"}},
		{"bad node name", func(c *Cluster) { c.Nodes[0].Name = "Node_1" }, []string{`nodes[0].name: "Node_1" must match ^[a-z0-9][a-z0-9-]{0,62}$`, `ui.node: "tscale-01" is not a name in nodes`}},
		{"not IPv4", func(c *Cluster) { c.Nodes[0].Address = "tscale-01.lan" }, []string{`nodes[0].address: "tscale-01.lan" is not an IPv4 address`}},
		{"every problem at once", func(c *Cluster) { c.Shards = 0; c.Nodes[1].Address = "10.78.0.11" }, []string{"shards: 0 must be between 1 and 16384", "nodes[1].address: 10.78.0.11 is also nodes[0]"}},
		{"bad temporal version", func(c *Cluster) { c.Temporal.Version = "1.31" }, []string{`temporal.version: "1.31" must be a version like 1.31.3`}},
		{"unsupported temporal version", func(c *Cluster) { c.Temporal.Version = "1.29.4" }, []string{`temporal.version: 1.29.4 is not supported; use 1.30.x or 1.31.x`}},
		{"missing cli", func(c *Cluster) { c.Temporal.CLI = "" }, []string{`temporal.cli: "" must be a version like 1.9.1`}},
		{"bad log level", func(c *Cluster) { c.Temporal.LogLevel = "verbose" }, []string{`temporal.logLevel: "verbose" must be one of debug, info, warn, error`}},
		{"no persistence", func(c *Cluster) { c.Persistence.Postgres = nil }, []string{"persistence: postgres is required (v1 supports only postgres)"}},
		{"postgres host by DNS name", func(c *Cluster) { c.Persistence.Postgres.Host = "db.lab.internal" }, nil},
		{"bad postgres host", func(c *Cluster) { c.Persistence.Postgres.Host = "db host" }, []string{`persistence.postgres.host: "db host" must be an IPv4 address or a DNS name`}},
		{"IPv6 postgres host", func(c *Cluster) { c.Persistence.Postgres.Host = "::1" }, []string{`persistence.postgres.host: "::1" must be an IPv4 address or a DNS name`}},
		{"bad postgres port", func(c *Cluster) { c.Persistence.Postgres.Port = 70000 }, []string{"persistence.postgres.port: 70000 must be between 1 and 65535"}},
		{"bad postgres user", func(c *Cluster) { c.Persistence.Postgres.User = "temporal-user" }, []string{`persistence.postgres.user: "temporal-user" must match ^[A-Za-z_][A-Za-z0-9_]{0,62}$`}},
		{"missing ssh user", func(c *Cluster) { c.SSH.User = "" }, []string{"ssh.user: is required"}},
		{"bad ssh port", func(c *Cluster) { c.SSH.Port = -1 }, []string{"ssh.port: -1 must be between 1 and 65535"}},
		{"access CIDR", func(c *Cluster) { c.Access.Clients = []string{"10.78.0.0/24"} }, nil},
		{"bad access source", func(c *Cluster) { c.Access.Monitors = []string{"10.78.0.1", "everyone"} }, []string{`access.monitors[1]: "everyone" must be an IPv4 address or CIDR`}},
		{"duplicate access source", func(c *Cluster) { c.Access.Admins = []string{"10.78.0.1", "10.78.0.1"} }, []string{`access.admins[1]: 10.78.0.1 is also access.admins[0]`}},
		{"no ui", func(c *Cluster) { c.UI = nil }, nil},
		{"ui on unknown node", func(c *Cluster) { c.UI.Node = "tscale-09" }, []string{`ui.node: "tscale-09" is not a name in nodes`}},
		{"missing ui version", func(c *Cluster) { c.UI.Version = "" }, []string{`ui.version: "" must be a version like 2.54.1`}},
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
