package fleet

import (
	"errors"
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
