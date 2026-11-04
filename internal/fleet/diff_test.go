package fleet

import (
	"reflect"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/settings"
)

// load is Load of an environment of root that must load.
func load(t *testing.T, root, name string) *Env {
	t.Helper()
	env, err := Load(root, name)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestDiff(t *testing.T) {
	d, err := Diff(load(t, fixture, "lab"), load(t, fixture, "staging"))
	if err != nil {
		t.Fatal(err)
	}

	want := []Difference{
		{Path: "namespaces.drills", From: "24h", To: nil},
		{Path: "overrides.frontend.namespaceRPS", From: []settings.Value{
			{Value: 400, Constraints: map[string]any{"namespace": "drills"}},
			{Value: 2400},
		}, To: nil},
	}
	if !reflect.DeepEqual(d.Promotable, want) {
		t.Errorf("promotable =\n%#v\nwant\n%#v", d.Promotable, want)
	}

	// temporal.logLevel is set in lab only, but staging gets the same default: not a difference.
	var paths []string
	for _, diff := range d.ByDesign {
		paths = append(paths, diff.Path)
	}
	for _, path := range []string{"cluster", "nodes", "persistence.postgres.host", "access.clients", "ssh.key"} {
		if !slices.Contains(paths, path) {
			t.Errorf("by design has no %s: %v", path, paths)
		}
	}
	if slices.Contains(paths, "shards") {
		t.Errorf("both have 512 shards, but by design lists them: %v", paths)
	}
}

func TestDiffSame(t *testing.T) {
	d, err := Diff(load(t, fixture, "lab"), load(t, fixture, "lab"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Promotable) != 0 || len(d.ByDesign) != 0 {
		t.Errorf("diff of lab with lab = %+v, want nothing", d)
	}
}

func TestDiffChanged(t *testing.T) {
	// staging sets the key lab sets, to the default: the files differ, so it is a difference.
	root := labWith(t, "environments/staging/overrides.yaml", "frontend.namespaceRPS:\n  - value: 2400\n").Root
	lab, staging := load(t, root, "lab"), load(t, root, "staging")
	lab.Cluster.Temporal.LogLevel = "debug"

	d, err := Diff(lab, staging)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]Difference{
		"temporal.logLevel": {Path: "temporal.logLevel", From: "debug", To: "info"},
		"overrides.frontend.namespaceRPS": {Path: "overrides.frontend.namespaceRPS", From: []settings.Value{
			{Value: 400, Constraints: map[string]any{"namespace": "drills"}},
			{Value: 2400},
		}, To: []settings.Value{{Value: 2400}}},
	}
	for _, diff := range d.Promotable {
		if w, ok := want[diff.Path]; ok && !reflect.DeepEqual(diff, w) {
			t.Errorf("%s =\n%#v\nwant\n%#v", diff.Path, diff, w)
		}
		delete(want, diff.Path)
	}
	if len(want) > 0 {
		t.Errorf("promotable has no %v", want)
	}
}

func TestDiffErrors(t *testing.T) {
	bad := labWith(t, "base/common.yaml", "a: [\n")
	good := load(t, fixture, "staging")
	if _, err := Diff(bad, good); err == nil {
		t.Error("from with broken settings: err = nil")
	}
	if _, err := Diff(good, bad); err == nil {
		t.Error("to with broken settings: err = nil")
	}
	bad.Cluster.Temporal.Version = "latest"
	if _, err := Diff(bad, good); err == nil {
		t.Error("from without a catalog: err = nil")
	}
}
