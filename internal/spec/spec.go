// Package spec loads and validates cluster.yaml.
package spec

// Cluster is the desired state of one Temporal cluster: the contents of cluster.yaml.
type Cluster struct {
	Cluster     string      `yaml:"cluster"`
	Temporal    Temporal    `yaml:"temporal"`
	Shards      int         `yaml:"shards"`
	Persistence Persistence `yaml:"persistence"`
	SSH         SSH         `yaml:"ssh"`
	Access      Access      `yaml:"access"`
	Namespaces  []Namespace `yaml:"namespaces"`
	Nodes       []Node      `yaml:"nodes"`
}

// Temporal selects the Temporal server and CLI releases.
type Temporal struct {
	Version  string `yaml:"version"`
	CLI      string `yaml:"cli"`
	LogLevel string `yaml:"logLevel"`
}

// Persistence names the datastore. Exactly one kind is set.
type Persistence struct {
	Postgres *Postgres `yaml:"postgres"`
}

// Postgres is how Temporal reaches its PostgreSQL server.
type Postgres struct {
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	User          string `yaml:"user"`
	TLS           bool   `yaml:"tls"`
	AdminDatabase string `yaml:"adminDatabase"`
}

// SSH is how tops reaches the nodes.
type SSH struct {
	User       string `yaml:"user"`
	Port       int    `yaml:"port"`
	Key        string `yaml:"key"`
	KnownHosts string `yaml:"knownHosts"`
}

// Access lists the sources allowed through each node's firewall.
type Access struct {
	Clients      []string `yaml:"clients"`
	Monitors     []string `yaml:"monitors"`
	Admins       []string `yaml:"admins"`
	Healthchecks []string `yaml:"healthchecks"`
}

// Namespace is a Temporal namespace tops keeps in place.
type Namespace struct {
	Name      string `yaml:"name"`
	Retention string `yaml:"retention"`
}

// UI places the Temporal Web UI on one node.
type UI struct {
	Node    string `yaml:"node"`
	Version string `yaml:"version"`
}

// Node is one host running Temporal.
type Node struct {
	Name    string   `yaml:"name"`
	Address string   `yaml:"address"`
	Roles   []string `yaml:"roles"`
}
