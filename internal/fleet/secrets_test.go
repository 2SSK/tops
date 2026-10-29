package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/spec"
	"github.com/2SSK/tops/internal/term"
)

const secretsFile = "environments/qa/secrets.env"

func TestParseSecrets(t *testing.T) {
	data := "# secrets.env - qa. Mode 0600. Never commit.\n\nTOPS_PG_PASSWORD=pa.ss_w~o-rd9\n"

	s, err := parseSecrets(secretsFile, []byte(data))
	if err != nil {
		t.Fatalf("parseSecrets: %#v", err)
	}
	if got := s["TOPS_PG_PASSWORD"].Reveal(); got != "pa.ss_w~o-rd9" {
		t.Errorf("TOPS_PG_PASSWORD = %q", got)
	}
}

func TestParseSecretsErrors(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantWhat string
		wantWhy  string
	}{
		{"no equals sign", "# x\nhunter2\n", "line 2", "expected KEY=VALUE"},
		{"unknown key", "TOPS_PG_PASWORD=hunter2\n", "line 1", `"TOPS_PG_PASWORD" is not a known key (TOPS_PG_PASSWORD)`},
		{"export", "export TOPS_PG_PASSWORD=hunter2\n", "line 1", `"export TOPS_PG_PASSWORD" is not a known key (TOPS_PG_PASSWORD)`},
		{"set twice", "TOPS_PG_PASSWORD=a\nTOPS_PG_PASSWORD=hunter2\n", "line 2", "TOPS_PG_PASSWORD is also set on line 1"},
		{"$ in value", "TOPS_PG_PASSWORD=hunter2$HOME\n", "line 1", "TOPS_PG_PASSWORD may only contain A-Z a-z 0-9 . _ ~ -"},
		{"quotes", "TOPS_PG_PASSWORD=\"hunter2\"\n", "line 1", "TOPS_PG_PASSWORD may only contain A-Z a-z 0-9 . _ ~ -"},
		{"trailing space", "TOPS_PG_PASSWORD=hunter2 \n", "line 1", "TOPS_PG_PASSWORD may only contain A-Z a-z 0-9 . _ ~ -"},
		{"empty value", "TOPS_PG_PASSWORD=\n", "line 1", "TOPS_PG_PASSWORD may only contain A-Z a-z 0-9 . _ ~ -"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSecrets(secretsFile, []byte(tt.data))

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != "secrets.invalid" {
				t.Fatalf("err = %#v, want a secrets.invalid UserError", err)
			}
			if want := secretsFile + " " + tt.wantWhat; ue.What != want || ue.Why != tt.wantWhy {
				t.Errorf("what = %q, why = %q\nwant  %q, %q", ue.What, ue.Why, want, tt.wantWhy)
			}
			if strings.Contains(ue.What+ue.Why+ue.Fix, "hunter2") {
				t.Errorf("the error shows the value: %#v", ue)
			}
		})
	}
}

func TestCheckRequired(t *testing.T) {
	pg := &spec.Cluster{Persistence: spec.Persistence{Postgres: &spec.Postgres{}}}

	if err := checkRequired(secretsFile, Secrets{}, &spec.Cluster{}); err != nil {
		t.Errorf("no datastore: err = %v, want nil", err)
	}

	s, err := parseSecrets(secretsFile, []byte("TOPS_PG_PASSWORD=x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRequired(secretsFile, s, pg); err != nil {
		t.Errorf("password set: err = %v, want nil", err)
	}

	var ue *term.UserError
	err = checkRequired(secretsFile, Secrets{}, pg)
	if !errors.As(err, &ue) || ue.Code != "secrets.invalid" || ue.What != secretsFile+" has no TOPS_PG_PASSWORD" {
		t.Errorf("err = %#v, want secrets.invalid naming TOPS_PG_PASSWORD", err)
	}
}

// qaEnv is an environment qa whose spec needs TOPS_PG_PASSWORD, in a new directory.
func qaEnv(t *testing.T) *Env {
	t.Helper()
	return &Env{
		Name:    "qa",
		Dir:     t.TempDir(),
		File:    "environments/qa/cluster.yaml",
		Cluster: &spec.Cluster{Persistence: spec.Persistence{Postgres: &spec.Postgres{}}},
	}
}

// writeSecrets writes e's secrets.env with mode. git does not keep 0600, so tests
// make the file they need.
func writeSecrets(t *testing.T, e *Env, data string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(e.Dir, "secrets.env")
	must(t, os.WriteFile(path, []byte(data), 0o600))
	must(t, os.Chmod(path, mode))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadSecrets(t *testing.T) {
	e := qaEnv(t)
	writeSecrets(t, e, "TOPS_PG_PASSWORD=hunter2\n", 0o600)

	s, err := e.ReadSecrets()
	if err != nil {
		t.Fatalf("ReadSecrets: %#v", err)
	}
	if got := s["TOPS_PG_PASSWORD"].Reveal(); got != "hunter2" {
		t.Errorf("TOPS_PG_PASSWORD = %q", got)
	}
}

func TestReadSecretsErrors(t *testing.T) {
	notRegular := "it is not a regular file (symlinks are not followed)"

	tests := []struct {
		name     string
		setup    func(t *testing.T, e *Env)
		wantCode string
		wantWhy  string
	}{
		{"missing", func(*testing.T, *Env) {}, "secrets.invalid", "no such file or directory"},
		{"mode 0644", func(t *testing.T, e *Env) {
			writeSecrets(t, e, "TOPS_PG_PASSWORD=hunter2\n", 0o644)
		}, "secrets.mode", "mode 0644, must be 0600"},
		{"mode 0400", func(t *testing.T, e *Env) {
			writeSecrets(t, e, "TOPS_PG_PASSWORD=hunter2\n", 0o400)
		}, "secrets.mode", "mode 0400, must be 0600"},
		{"symlink", func(t *testing.T, e *Env) {
			target := filepath.Join(t.TempDir(), "secrets.env")
			must(t, os.WriteFile(target, []byte("TOPS_PG_PASSWORD=hunter2\n"), 0o600))
			must(t, os.Symlink(target, filepath.Join(e.Dir, "secrets.env")))
		}, "secrets.mode", notRegular},
		{"directory", func(t *testing.T, e *Env) {
			must(t, os.Mkdir(filepath.Join(e.Dir, "secrets.env"), 0o700))
		}, "secrets.mode", notRegular},
		{"bad line", func(t *testing.T, e *Env) {
			writeSecrets(t, e, "hunter2\n", 0o600)
		}, "secrets.invalid", "expected KEY=VALUE"},
		{"missing key", func(t *testing.T, e *Env) {
			writeSecrets(t, e, "# no keys yet\n", 0o600)
		}, "secrets.invalid", "persistence.postgres needs its password"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := qaEnv(t)
			tt.setup(t, e)

			_, err := e.ReadSecrets()

			var ue *term.UserError
			if !errors.As(err, &ue) || ue.Code != tt.wantCode || ue.Why != tt.wantWhy {
				t.Fatalf("err = %#v, want %s with why %q", err, tt.wantCode, tt.wantWhy)
			}
			if !strings.Contains(ue.What, "environments/qa/secrets.env") {
				t.Errorf("what = %q, want the file relative to the fleet root", ue.What)
			}
			if strings.Contains(ue.What+ue.Why+ue.Fix, "hunter2") {
				t.Errorf("the error shows the value: %#v", ue)
			}
		})
	}
}

func TestCheckPrivateOwner(t *testing.T) {
	e := qaEnv(t)
	writeSecrets(t, e, "TOPS_PG_PASSWORD=x\n", 0o600)
	info, err := os.Lstat(filepath.Join(e.Dir, "secrets.env"))
	must(t, err)

	uid := os.Getuid()
	if err := checkPrivate(secretsFile, info, uid); err != nil {
		t.Errorf("own file: err = %v, want nil", err)
	}

	var ue *term.UserError
	err = checkPrivate(secretsFile, info, uid+1)
	if !errors.As(err, &ue) || ue.Code != "secrets.mode" || !strings.Contains(ue.Why, "owned by another user") {
		t.Errorf("err = %#v, want secrets.mode about the owner", err)
	}
}
