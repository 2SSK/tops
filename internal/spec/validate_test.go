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
		{"duplicate address", func(c *Cluster) { c.Nodes[1].Address = "10.80.0.11" }, []string{"nodes[1].address: 10.80.0.11 is also nodes[0]"}},
		{"duplicate name", func(c *Cluster) { c.Nodes[1].Name = "vps-01" }, []string{"nodes[1].name: vps-01 is also nodes[0]"}},
		{"bad node name", func(c *Cluster) { c.Nodes[0].Name = "Node_1" }, []string{`nodes[0].name: "Node_1" must match ^[a-z0-9][a-z0-9-]{0,62}$`}},
		{"not IPv4", func(c *Cluster) { c.Nodes[0].Address = "vps-01.lan" }, []string{`nodes[0].address: "vps-01.lan" is not an IPv4 address`}},

		{"every problem at once", func(c *Cluster) { c.Shards = 0; c.Nodes[1].Address = "10.80.0.11" }, []string{"shards: 0 must be between 1 and 16384", "nodes[1].address: 10.80.0.11 is also nodes[0]"}},

		{"missing roles", func(c *Cluster) { c.Nodes[2].Roles = nil }, []string{"nodes[2].roles: at least one role is required (frontend, history, matching, worker)"}},
		{"empty roles", func(c *Cluster) { c.Nodes[2].Roles = []string{} }, []string{"nodes[2].roles: at least one role is required (frontend, history, matching, worker)"}},
		{"unknown roles", func(c *Cluster) { c.Nodes[2].Roles[1] = "matchng" }, []string{`nodes[2].roles[1]: "matchng" is not a role (frontend, history, matching, worker)`}},
		{"capitalised roles", func(c *Cluster) { c.Nodes[2].Roles[0] = "Frontend" }, []string{`nodes[2].roles[0]: "Frontend" is not a role (frontend, history, matching, worker)`}},
		{"duplicate roles", func(c *Cluster) { c.Nodes[1].Roles = []string{"frontend", "history", "history"} }, []string{"nodes[1].roles[2]: history is also nodes[1].roles[1]"}},
		{"ui is not a role yet", func(c *Cluster) { c.Nodes[0].Roles = append(c.Nodes[0].Roles, "ui") }, []string{`nodes[0].roles[4]: "ui" is not a role (frontend, history, matching, worker)`}},
		{"no history anywhere", func(c *Cluster) {
			c.Nodes[0].Roles = []string{"frontend", "matching", "worker"}
			c.Nodes[1].Roles = []string{"frontend", "matching", "worker"}
		}, []string{"nodes: no node runs history"}},

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
		{"access CIDR", func(c *Cluster) { c.Access.Clients = []string{"10.80.0.0/24"} }, nil},
		{"bad access source", func(c *Cluster) { c.Access.Monitors = []string{"10.80.0.1", "everyone"} }, []string{`access.monitors[1]: "everyone" must be an IPv4 address or CIDR`}},
		{"duplicate access source", func(c *Cluster) { c.Access.Admins = []string{"10.80.0.1", "10.80.0.1"} }, []string{`access.admins[1]: 10.80.0.1 is also access.admins[0]`}},

		{"bad healthchecks source", func(c *Cluster) { c.Access.Healthchecks = []string{"lb"} }, []string{`access.healthchecks[0]: "lb" must be an IPv4 address or CIDR`}},
		{"duplicate healthchecks source", func(c *Cluster) { c.Access.Healthchecks = []string{"10.80.0.1", "10.80.0.1"} }, []string{`access.healthchecks[1]: 10.80.0.1 is also access.healthchecks[0]`}},

		{"retention in days", func(c *Cluster) { c.Namespaces[0].Retention = "7d" }, nil},
		{"bad retention", func(c *Cluster) { c.Namespaces[0].Retention = "a week" }, []string{`namespaces[0].retention: "a week" is not a duration like 72h or 7d`}},
		{"missing retention", func(c *Cluster) { c.Namespaces[1].Retention = "" }, []string{`namespaces[1].retention: "" is not a duration like 72h or 7d`}},
		{"retention too short", func(c *Cluster) { c.Namespaces[0].Retention = "30m" }, []string{"namespaces[0].retention: 30m is below Temporal's minimum of 1h"}},

		{"bad namespace name", func(c *Cluster) { c.Namespaces[0].Name = "my namespace" }, []string{`namespaces[0].name: "my namespace" must match ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,254}$`}},
		{"duplicate namespace", func(c *Cluster) { c.Namespaces[1].Name = "default" }, []string{"namespaces[1].name: default is also namespaces[0]"}},
		{"system namespace", func(c *Cluster) { c.Namespaces[0].Name = "temporal-system" }, []string{"namespaces[0].name: temporal-system is Temporal's own namespace and cannot be managed"}},
		{"missing namespace name", func(c *Cluster) { c.Namespaces[0].Name = "" }, []string{"namespaces[0].name: is required"}},
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
