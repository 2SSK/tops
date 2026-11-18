package render

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/2SSK/tops/internal/catalog"
	"github.com/2SSK/tops/internal/settings"
)

// catalog131 is loaded once: lab() parses its dynamic config on every call.
var catalog131 = sync.OnceValue(func() *catalog.Catalog {
	c, err := catalog.Load("1.31")
	if err != nil {
		panic(err)
	}
	return c
})

// dynamic parses a fleet dynamic-config file the way validate does. The tests' files are
// valid, so a problem is a broken test.
func dynamic(src string) settings.File {
	f, problems, err := settings.Parse("base/test.yaml", []byte(src), catalog131())
	if err != nil || len(problems) > 0 {
		panic(fmt.Sprint(err, problems))
	}
	return f
}

const header = "# Rendered by tops for lab/tops-e2e/vps-01 role %s. tops apply overwrites hand edits.\n"

// TestDynamicConfigMatchesSchema: the backend schema's example, written back in Temporal's
// format, the fallback after the constrained value as the fleet wrote it.
func TestDynamicConfigMatchesSchema(t *testing.T) {
	files := mustRender(t, lab())
	want := fmt.Sprintf(header, "frontend") + `frontend.namespaceRPS:
  - value: 400
    constraints:
      namespace: drills
  - value: 2400
`
	if got := string(file(t, files, "/etc/temporal/dynamicconfig-frontend.yaml").Content); got != want {
		t.Errorf("dynamicconfig-frontend.yaml:\n%s\nwant\n%s", got, want)
	}
	if got, want := string(file(t, files, "/etc/temporal/dynamicconfig-history.yaml").Content), fmt.Sprintf(header, "history")+"history.cacheTTL:\n  - value: 15m\n"; got != want {
		t.Errorf("dynamicconfig-history.yaml:\n%s\nwant\n%s", got, want)
	}
}

// TestDynamicConfigRoundTrip: whatever the fleet wrote, of every type, reads back as the
// same settings. Keys come out in the catalog's spelling and sorted, comments and the
// fleet's key order do not survive, and a string that looks like a bool stays a string.
func TestDynamicConfigRoundTrip(t *testing.T) {
	in := lab()
	in.Roles[1].Dynamic = dynamic(`
# comments are the fleet's, not the node's
history.cacheTTL:
  - value: 15m
HISTORY.maxBufferedQueryCount:          # spelled differently: the catalog's spelling is rendered
  - value: 2
system.secondaryVisibilityWritingMode:
  - value: "yes"                        # a string, though YAML 1.1 reads yes as true
history.ReplicationExecutableTaskErrorRetryBackoffCoefficient:
  - value: 1.5
history.EnableHistoryReplicationRateLimiter:
  - value: true
    constraints: { namespace: drills }
  - value: false
history.timerProcessorSchedulerActiveRoundRobinWeights:
  - value: { 1: 10, 2: 5 }
`)
	got := file(t, mustRender(t, in), "/etc/temporal/dynamicconfig-history.yaml").Content
	if !strings.HasPrefix(string(got), fmt.Sprintf(header, "history")) {
		t.Errorf("no header:\n%s", got)
	}
	back := dynamic(string(got))
	if len(back) != len(in.Roles[1].Dynamic) {
		t.Fatalf("%d keys came back, want %d:\n%s", len(back), len(in.Roles[1].Dynamic), got)
	}
	for key, e := range in.Roles[1].Dynamic {
		if !reflect.DeepEqual(back[key].Values, e.Values) {
			t.Errorf("%s: %v came back as %v", key, e.Values, back[key].Values)
		}
	}
	if !strings.Contains(string(got), "history.MaxBufferedQueryCount:") {
		t.Errorf("the key is not in the catalog's spelling:\n%s", got)
	}
	if keys := topLevelKeys(got); !slices.IsSorted(keys) {
		t.Errorf("keys not sorted: %v", keys)
	}
}

// topLevelKeys are the lines of a rendered file that start a key.
func topLevelKeys(file []byte) []string {
	keys := []string{}
	for _, line := range strings.Split(string(file), "\n") {
		if line != "" && line[0] != ' ' && line[0] != '#' {
			keys = append(keys, line)
		}
	}
	return keys
}

// TestDynamicConfigEmpty: a role nobody configured gets an empty map, which Temporal reads as
// no overrides, every default in force.
func TestDynamicConfigEmpty(t *testing.T) {
	in := lab()
	in.Roles[1].Dynamic = nil
	got := string(file(t, mustRender(t, in), "/etc/temporal/dynamicconfig-history.yaml").Content)
	if want := fmt.Sprintf(header, "history") + "{}\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if len(dynamic(got)) != 0 {
		t.Error("{} reads back as settings")
	}
}
