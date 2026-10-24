package spec

import "testing"

func TestDefaultsFillsEmptyFields(t *testing.T) {
	c := Cluster{Persistence: Persistence{Postgres: &Postgres{}}}
	Defaults(&c)

	checks := []struct {
		path string
		got  any
		want any
	}{
		{"temporal.logLevel", c.Temporal.LogLevel, "info"},
		{"ssh.port", c.SSH.Port, 22},
		{"ssh.knownHosts", c.SSH.KnownHosts, "~/.ssh/known_hosts"},
		{"persistence.postgres.port", c.Persistence.Postgres.Port, 5432},
		{"persistence.postgres.adminDatabase", c.Persistence.Postgres.AdminDatabase, "postgres"},
	}

	for _, ch := range checks {
		if ch.got != ch.want {
			t.Errorf("%s = %v, want %v", ch.path, ch.got, ch.want)
		}
	}
}

func TestDefaultsKeepsSetFields(t *testing.T) {
	c := Cluster{
		Temporal:    Temporal{LogLevel: "debug"},
		SSH:         SSH{Port: 2222},
		Persistence: Persistence{Postgres: &Postgres{Port: 6432}},
	}
	Defaults(&c)

	if c.Temporal.LogLevel != "debug" || c.SSH.Port != 2222 || c.Persistence.Postgres.Port != 6432 {
		t.Errorf("Defaults() overwrote set values: %+v", c)
	}
}

func TestDefaultsWithoutPostgres(t *testing.T) {
	var c Cluster
	Defaults(&c)

	if c.Persistence.Postgres != nil {
		t.Errorf("Defaults() created a Postgres section: %+v", c.Persistence.Postgres)
	}
}
