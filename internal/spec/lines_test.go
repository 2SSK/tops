package spec

import (
	"os"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestLineOf(t *testing.T) {
	data, err := os.ReadFile("testdata/lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want int
	}{
		{"cluster", 1},
		{"temporal.cli", 4},
		{"persistence.postgres.user", 11},
		{"nodes", 25},
		{"nodes[2]", 32},
		{"nodes[1].address", 30},
		{"nodes[2].roles[1]", 34},
		{"access.clients[0]", 18},
		{"namespaces[1].retention", 24},
		{"", 1},
		{"ssh.port", 13},
		{"persistence.postgres.adminDatabase", 8},
		{"nodes[5].name", 25},
		{"access.healthchecks[0]", 21},
		{"cluster.name", 1},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := lineOf(&doc, tt.path); got != tt.want {
				t.Errorf("lineOf(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}
