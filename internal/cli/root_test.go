package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpShowsGroups(t *testing.T) {
	cmd := NewRootCommand(&Options{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	for _, group := range []string{"Spec:", "Change:", "Look:", "Integrate:"} {
		if !strings.Contains(out.String(), group) {
			t.Errorf("help is missing group %q\n%s", group, out.String())
		}
	}
}
