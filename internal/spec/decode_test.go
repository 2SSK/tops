package spec

import (
	"os"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestDecodeProblems(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want []Problem
	}{
		{"unknown key", "cluster: a\nshard: 512\n", []Problem{{Path: "shard", Line: 2, Message: "unknown key"}}},
		{"profile is gone", "cluster: a\nprofile: 2c4g\n", []Problem{{Path: "profile", Line: 2, Message: "unknown key"}}},
		{"ui is gone", "cluster: a\nui:\n version: 2.54.1\n", []Problem{{Path: "ui", Line: 2, Message: "unknown key"}}},
		{"unknown key is a flow mapping", "nodes:\n   - { name: x, addr: 10.0.0.1 }\n", []Problem{{Path: "nodes[0].addr", Line: 2, Message: "unknown key"}}},
		{"text for a number", "shards: many\n", []Problem{{Path: "shards", Line: 1, Message: `want a number, got "many"`}}},
		{"text for a bool", "persistence:\n  postgres:\n    tls: maybe\n", []Problem{{Path: "persistence.postgres.tls", Line: 3, Message: `want true or false, got "maybe"`}}},
		{"list for a number", "ssh:\n  port: [22]\n", []Problem{{Path: "ssh.port", Line: 2, Message: "want a number, got a list"}}},
		{"mapping for a string", "cluster: { name: a }\n", []Problem{{Path: "cluster", Line: 1, Message: "want a string, got a mapping"}}},
		{"number for a list", "nodes: 5\n", []Problem{{Path: "nodes", Line: 1, Message: `want a list, got "5"`}}},
		{"not a mapping", "hello\n", []Problem{{Path: "", Line: 1, Message: `want a mapping, got "hello"`}}},
		{"two on one line", "nodes:\n  - { name: x, addr: 10.0.0.1, roles: frontend }\n", []Problem{
			{Path: "nodes[0].addr", Line: 2, Message: "unknown key"},
			{Path: "nodes[0].roles", Line: 2, Message: `want a list, got "frontend"`},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Parse("cluster.yaml", []byte(tt.yaml))
			if err != nil {
				t.Fatalf("Parse: %#v", err)
			}
			if !slices.Equal(c.decoded, tt.want) {
				t.Errorf("decoded = %+v\nwant			%+v", c.decoded, tt.want)
			}
		})
	}
}

// TestDecodeProblemFormat pins the yaml.TypeError entry format that decodeProblem parses,
// and what it does with an entry it does not recognise.
func TestDecodeProblemFormat(t *testing.T) {
	var doc yaml.Node
	tests := []struct {
		entry string
		want  Problem
	}{
		{"line 7: something new", Problem{Line: 7, Message: "something new"}},
		{"no line at all", Problem{Message: "no line at all"}},
	}

	for _, tt := range tests {
		t.Run(tt.entry, func(t *testing.T) {
			if got := decodeProblem(&doc, tt.entry); got != tt.want {
				t.Errorf("decodeProblem = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestValidateReportsDecodeProblems(t *testing.T) {
	data, err := os.ReadFile("testdata/lab.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(data), "shards: 512", "shards: many", 1)

	c, err := Parse("testdata/lab.yaml", []byte(bad))
	if err != nil {
		t.Fatalf("Parse: %#v", err)
	}
	Defaults(c)

	// shards is 0 after the failed decode; "0 must be between 1 and 16384" is left out.
	want := []Problem{{Path: "shards", Line: 6, Message: `want a number, got "many"`}}
	if ps := Validate(c); !slices.Equal(ps, want) {
		t.Errorf("Validate = %+v, want %+v", ps, want)
	}
}
