package plan

import (
	"reflect"
	"testing"

	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/wire"
)

// TestValidate: the unit, and one entry per role, in the files' order, with its server config,
// its dynamic config, its drop-in, and the environment systemd gives it: temporal.env's lines,
// then the role's own, without comments or blank lines. The secret file's keys are the ones
// the helper masks.
func TestValidate(t *testing.T) {
	fs := append(files(),
		render.File{Path: "/etc/systemd/system/temporal@.service", Content: []byte("[Unit]\n")},
		render.File{Path: "/etc/temporal/frontend.yaml", Content: []byte("# enable-template\nfrontend: 1\n"), Role: "frontend"},
		render.File{Path: "/etc/temporal/dynamicconfig-frontend.yaml", Content: []byte("frontend.x: 1\n"), Role: "frontend"},
		render.File{Path: "/etc/systemd/system/temporal@frontend.service.d/limits.conf", Content: []byte("[Service]\n"), Role: "frontend"},
		render.File{Path: "/etc/temporal/history.yaml", Content: []byte("history: 1\n"), Role: "history"},
	)
	got := Validate("1.31.3", fs)
	want := wire.Validate{Version: "1.31.3", Unit: "[Unit]\n", Roles: []wire.RoleFiles{
		{
			Role: "frontend", Config: "# enable-template\nfrontend: 1\n", DynamicConfig: "frontend.x: 1\n", Limits: "[Service]\n",
			Env:     []string{"TOPS_PG_PASSWORD=" + password, "GOMEMLIMIT=400MiB"},
			Secrets: []string{"TOPS_PG_PASSWORD"},
		},
		{
			Role: "history", Config: "history: 1\n", DynamicConfig: "", Limits: "", // none rendered: empty files
			Env:     []string{"TOPS_PG_PASSWORD=" + password, "GOMEMLIMIT=1000MiB"},
			Secrets: []string{"TOPS_PG_PASSWORD"},
		},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Validate =\n%+v\nwant\n%+v", got, want)
	}
}

// TestValidateNoRoles: a node with only shared files has nothing for temporal-server to check.
func TestValidateNoRoles(t *testing.T) {
	if got := Validate("1.31.3", files()[:1]); len(got.Roles) != 0 || got.Roles == nil {
		t.Errorf("roles = %#v, want an empty list", got.Roles)
	}
}
