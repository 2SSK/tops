package render

import (
	"bytes"
	"io/fs"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/2SSK/tops/internal/secret"
)

func mustRender(t *testing.T, in NodeInput) FileSet {
	t.Helper()
	files, err := Render(in)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func file(t *testing.T, files FileSet, path string) File {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no %s", path)
	return File{}
}

// TestRenderFileSet: every file of vps-01, in order, with the owner, mode and effect the
// backend schema's "Files on a node" table gives it.
func TestRenderFileSet(t *testing.T) {
	type meta struct {
		Path         string
		Mode         fs.FileMode
		Owner, Group string
		Role         string
		Effect       Effect
	}
	var got []meta
	for _, f := range mustRender(t, lab()) {
		got = append(got, meta{f.Path, f.Mode, f.Owner, f.Group, f.Role, f.Effect})
	}
	want := []meta{
		{"/etc/systemd/system/temporal@.service", 0o644, "root", "root", "", RestartAll},
		{"/etc/temporal/temporal.env", 0o600, "root", "root", "", RestartAll},
		{"/etc/temporal/frontend.yaml", 0o640, "root", "temporal", "frontend", RestartRole},
		{"/etc/temporal/frontend.env", 0o640, "root", "temporal", "frontend", RestartRole},
		{"/etc/systemd/system/temporal@frontend.service.d/limits.conf", 0o644, "root", "root", "frontend", RestartRole},
		{"/etc/temporal/dynamicconfig-frontend.yaml", 0o640, "root", "temporal", "frontend", Dynamic},
		{"/etc/temporal/history.yaml", 0o640, "root", "temporal", "history", RestartRole},
		{"/etc/temporal/history.env", 0o640, "root", "temporal", "history", RestartRole},
		{"/etc/systemd/system/temporal@history.service.d/limits.conf", 0o644, "root", "root", "history", RestartRole},
		{"/etc/temporal/dynamicconfig-history.yaml", 0o640, "root", "temporal", "history", Dynamic},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file set:\n got %v\nwant %v", got, want)
	}
}

// TestRenderGolden pins the bytes of every file kind on vps-01.
func TestRenderGolden(t *testing.T) {
	files := mustRender(t, lab())
	for path, golden := range map[string]string{
		"/etc/systemd/system/temporal@.service":                      "temporal@.service",
		"/etc/temporal/temporal.env":                                 "vps-01-temporal.env",
		"/etc/temporal/history.env":                                  "vps-01-history.env",
		"/etc/systemd/system/temporal@history.service.d/limits.conf": "vps-01-history-limits.conf",
		"/etc/temporal/dynamicconfig-frontend.yaml":                  "vps-01-dynamicconfig-frontend.yaml",
		"/etc/temporal/dynamicconfig-history.yaml":                   "vps-01-dynamicconfig-history.yaml",
	} {
		t.Run(golden, func(t *testing.T) { checkGolden(t, golden, file(t, files, path).Content) })
	}
}

// TestRoleFilesMatchSchema: the backend schema's example history.env and limits.conf, with the
// same header as every rendered file.
func TestRoleFilesMatchSchema(t *testing.T) {
	files := mustRender(t, lab())
	header := "# Rendered by tops for lab/tops-e2e/vps-01 role history. tops apply overwrites hand edits.\n"
	if got, want := string(file(t, files, "/etc/temporal/history.env").Content), header+"GOMEMLIMIT=1000MiB\nGOMAXPROCS=2\n"; got != want {
		t.Errorf("history.env:\n%s\nwant\n%s", got, want)
	}
	limits := "/etc/systemd/system/temporal@history.service.d/limits.conf"
	if got, want := string(file(t, files, limits).Content), header+"[Service]\nMemoryHigh=1200M\nMemoryMax=1300M\nCPUWeight=150\n"; got != want {
		t.Errorf("limits.conf:\n%s\nwant\n%s", got, want)
	}
}

// TestUnitMatchesSchema: the unit runs the role named by its instance, as temporal, with the
// shared secrets and then the role's own environment.
func TestUnitMatchesSchema(t *testing.T) {
	unit := string(file(t, mustRender(t, lab()), "/etc/systemd/system/temporal@.service").Content)
	for _, want := range []string{
		"User=temporal\nGroup=temporal\n",
		"EnvironmentFile=/etc/temporal/temporal.env\nEnvironmentFile=/etc/temporal/%i.env\n",
		"ExecStart=/opt/temporal/current/temporal-server --config-file /etc/temporal/%i.yaml --allow-no-auth start --service %i\n",
		"TimeoutStopSec=90\n",
		"NoNewPrivileges=yes\nProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nReadWritePaths=/var/lib/temporal\n",
		"[Install]\nWantedBy=multi-user.target\n",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("temporal@.service has no\n%s", want)
		}
	}
}

// TestRenderByteStable: the same input renders the same bytes, so a hash says whether a file
// changed. Map order (the secrets) must not leak into the output.
func TestRenderByteStable(t *testing.T) {
	in := lab()
	in.Secrets = map[string]secret.Value{"TOPS_PG_PASSWORD": secret.New("p1"), "TOPS_ES_PASSWORD": secret.New("p2"), "TOPS_CASSANDRA_PASSWORD": secret.New("p3")}
	first := mustRender(t, in)
	for range 20 {
		if !reflect.DeepEqual(mustRender(t, in), first) {
			t.Fatal("two renders of the same input differ")
		}
	}
	want := "TOPS_CASSANDRA_PASSWORD=p3\nTOPS_ES_PASSWORD=p2\nTOPS_PG_PASSWORD=p1\n"
	if got := string(file(t, first, "/etc/temporal/temporal.env").Content); !strings.HasSuffix(got, want) {
		t.Errorf("temporal.env:\n%s\nwant the keys sorted:\n%s", got, want)
	}
}

// TestSecretsOnlyInTemporalEnv is the property: whatever the secret, its value appears in
// temporal.env, and in no other file. The values are drawn from what validate accepts, long
// enough that a match by chance is impossible.
func TestSecretsOnlyInTemporalEnv(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._~-"
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // a fixed seed, so a failure reproduces; the values are test data
	for i := range 200 {
		b := make([]byte, 16+rng.IntN(48))
		for j := range b {
			b[j] = alphabet[rng.IntN(len(alphabet))]
		}
		value := string(b)
		in := lab()
		in.Secrets = map[string]secret.Value{"TOPS_PG_PASSWORD": secret.New(value)}
		for _, f := range mustRender(t, in) {
			has := bytes.Contains(f.Content, b)
			if f.Path == "/etc/temporal/temporal.env" {
				if !has || !bytes.Contains(f.Content, []byte("\nTOPS_PG_PASSWORD="+value+"\n")) {
					t.Fatalf("case %d: temporal.env has no TOPS_PG_PASSWORD=%s line:\n%s", i, value, f.Content)
				}
				continue
			}
			if has {
				t.Fatalf("case %d: the secret is in %s", i, f.Path)
			}
		}
	}
}

// TestRenderTemplateError: a missing template fails the render and names it, rather than
// leaving a file empty. The embedded templates cannot be missing, so the test swaps in none.
func TestRenderTemplateError(t *testing.T) {
	saved := templates
	t.Cleanup(func() { templates = saved })
	templates = template.New("")
	if files, err := Render(lab()); err == nil || files != nil || !strings.Contains(err.Error(), "render temporal@.service") {
		t.Errorf("Render without templates = %d files, %v", len(files), err)
	}
}
