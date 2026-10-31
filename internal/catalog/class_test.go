package catalog

import (
	"strings"
	"testing"
)

func TestClass(t *testing.T) {
	c := load(t)
	if got := c.Class("history.cacheTTL"); got.Apply != "restart" || !strings.Contains(got.Evidence, "NewHostLevelCache") {
		t.Errorf("history.cacheTTl = %+v, want restart citing NewHostLevelCache", got)
	}
	if got := c.Class("frontend.shutdownDrainDuration"); got.Apply != "hot" || got.Lab != "pending" {
		t.Errorf("frontend.shutdownDrainDuration = %+v, want hot, lab pending", got)
	}
	if got := c.Class("history.eventsCacheTTL"); got != (Class{Apply: "unknown"}) {
		t.Errorf("an unclassified key = %+v, want unknown", got)
	}
}

func TestParseClassesErrors(t *testing.T) {
	c := load(t)
	tests := []struct {
		name, data, want string
	}{
		{"not in the catalog", "history.cacheTT:\n  apply: resetart\n  evidence: x\n", "history.cacheTT: not a setting of Temporal 1.31.3"},
		{"wrong case", "history.cachettl:\n  apply: restart\n  evidence: x\n", "history.cachettl: not a setting of Temporal 1.31.3"},
		{"bad apply", "history.cacheTTL:\n  apply: unknown\n  evidence: x\n", `apply is "unknown", want hot or restart`},
		{"no evidence", "history.cacheTTL:\n  apply: restart\n", "no evidence"},
		{"hot without lab", "history.cacheTTL:\n  apply: hot\n  evidence: x\n", "hot needs lab"},
		{"unknown field", "history.cacheTTL:\n  apply: restart\n  evidence: x\n  note: y\n", "field note not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseClasses("class-1.31.yaml", []byte(tt.data), c)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// fleetKeys are the keys temporalScale sets (config/dynamicconfig.yaml and the profiles'
// dynamicConfig), plust history.cacheTTL: every one needs a class before tops sets it.
var fleetKeys = []string{
	"frontend.shutdownDrainDuration",
	"frontend.shutdownFailHealthCheckDuration",
	"history.cacheSizeBasedLimit",
	"history.cacheTTL",
	"history.enableHostLevelEventsCache",
	"history.eventsHostLevelCacheMaxSizeBytes",
	"history.hostLevelCacheMaxSizeBytes",
	"history.shutdownDrainDuration",
	"limit.maxIDLength",
	"matching.shutdownDrainDuration",
}

func TestFleetKeysAreClassified(t *testing.T) {
	c := load(t)
	for _, key := range fleetKeys {
		if got := c.Class(key).Apply; got == "unknown" {
			t.Errorf("%s is set by the fleet but has no class", key)
		}
	}
}
