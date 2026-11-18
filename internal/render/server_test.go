package render

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"go.yaml.in/yaml/v3"

	"github.com/2SSK/tops/internal/sizing"
	"github.com/2SSK/tops/internal/spec"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// checkGolden compares got with testdata/name. After a deliberate change, rewrite the files
// with go test ./internal/render -update, and read every diff before committing.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path) //nolint:gosec // the package's own golden files
	if err != nil {
		t.Fatalf("%v (run go test ./internal/render -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file:\n%s", name, got)
	}
}

// lab is vps-01 of the e2e fixture with the backend schema's example sizes for history.
func lab() NodeInput {
	return NodeInput{
		Env: "lab", Cluster: "tops-e2e", Node: "vps-01", Address: "10.80.0.11", Shards: 512, LogLevel: "info",
		Postgres: spec.Postgres{Host: "10.80.0.20", Port: 5432, User: "temporal", AdminDatabase: "postgres"},
		Frontend: "127.0.0.1:7233",
		Roles: []RoleInput{
			{Role: "frontend", Size: sizing.RoleSize{Role: "frontend", DBPool: 2, DBVisPool: 3}},
			{Role: "history", Size: sizing.RoleSize{Role: "history", DBPool: 12, DBVisPool: 3}},
		},
	}
}

func role(t *testing.T, in NodeInput, name string) RoleInput {
	t.Helper()
	for _, r := range in.Roles {
		if r.Role == name {
			return r
		}
	}
	t.Fatalf("no %s on %s", name, in.Node)
	return RoleInput{}
}

func TestServerConfigGolden(t *testing.T) {
	in := lab()
	for _, r := range in.Roles {
		t.Run(r.Role, func(t *testing.T) {
			got, err := serverConfig(in, r)
			if err != nil {
				t.Fatal(err)
			}
			checkGolden(t, in.Node+"-"+r.Role+".yaml", got)
		})
	}
}

// TestServerConfigMatchesSchema: the history config is the backend schema's example, the file
// T2.1a ran by hand, except the header names no tops version (a new tops must not change
// every file and restart every role).
func TestServerConfigMatchesSchema(t *testing.T) {
	in := lab()
	got, err := serverConfig(in, role(t, in, "history"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(got), "\n")
	if lines[1] != "# Rendered by tops for lab/tops-e2e/vps-01 role history. tops apply overwrites hand edits." {
		t.Errorf("header = %q", lines[1])
	}
	for _, want := range []string{
		`        connectAddr: "10.80.0.20:5432"`,
		`        password: {{ env "TOPS_PG_PASSWORD" | quote }}`, // Temporal's template, passed through
		"        maxConns: 12\n        maxIdleConns: 12\n",
		"        maxConns: 3\n        maxIdleConns: 3\n",
		`  membership: { maxJoinDuration: 30s, broadcastAddress: "10.80.0.11" }`,
		"  pprof: { port: 7934, host: 127.0.0.1 }",
		`    prometheus: { framework: tally, timerType: histogram, listenAddress: "0.0.0.0:9234" }`,
		"  frontend: { rpc: { grpcPort: 7233, membershipPort: 6933, httpPort: 7243, bindOnIP: 0.0.0.0 } }\n" +
			"  history:  { rpc: { grpcPort: 7234, membershipPort: 6934, bindOnIP: 0.0.0.0 } }\n" +
			"  matching: { rpc: { grpcPort: 7235, membershipPort: 6935, bindOnIP: 0.0.0.0 } }\n" +
			"  worker:   { rpc: { grpcPort: 7239, membershipPort: 6939, bindOnIP: 0.0.0.0 } }\n",
		"    tops-e2e: { enabled: true, initialFailoverVersion: 1, rpcName: frontend, rpcAddress: 127.0.0.1:7233 }",
		"  filepath: /etc/temporal/dynamicconfig-history.yaml",
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("history.yaml has no\n%s", want)
		}
	}
}

// asTemporal reads a config the way temporal-server does with # enable-template: first Go's
// text/template with sprig's functions, then YAML. The raw file is not YAML by itself: the
// password is a template until the node expands it.
func asTemporal(t *testing.T, config []byte, env map[string]string) map[string]any {
	t.Helper()
	funcs := template.FuncMap{
		"env":   func(k string) string { return env[k] },
		"quote": func(s string) string { return fmt.Sprintf("%q", s) }, // sprig v3.3.0's quote, which Temporal 1.31.3 loads
	}
	tmpl, err := template.New("config").Funcs(funcs).Parse(string(config))
	if err != nil {
		t.Fatalf("Temporal's template pass: %v", err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, nil); err != nil {
		t.Fatalf("Temporal's template pass: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("not YAML after the template pass: %v\n%s", err, b.String())
	}
	return doc
}

// TestServerConfigIsTemporalYAML: Temporal sees enable-template (it must be in the first 1 KB),
// expands {{ env }}, and the result is YAML with the password in place.
func TestServerConfigIsTemporalYAML(t *testing.T) {
	in := lab()
	got, err := serverConfig(in, role(t, in, "history"))
	if err != nil {
		t.Fatal(err)
	}
	if i := bytes.Index(got, []byte("# enable-template")); i < 0 || i >= 1024 {
		t.Errorf("# enable-template at %d, want within the first 1 KB", i)
	}
	// Any password survives, whatever it contains: quote escapes it for YAML's double quotes.
	for _, password := range []string{"9f2c4e1b", `pa"ss`, `back\slash`, "has # hash", "colon: space", "pässwörd", "{{ not a template }}", ""} {
		doc := asTemporal(t, got, map[string]string{"TOPS_PG_PASSWORD": password})
		for _, store := range []string{"default", "visibility"} {
			sql := doc["persistence"].(map[string]any)["datastores"].(map[string]any)[store].(map[string]any)["sql"].(map[string]any)
			if sql["password"] != password {
				t.Errorf("%s store: password %q came back as %q", store, password, sql["password"])
			}
		}
	}
	doc := asTemporal(t, got, map[string]string{"TOPS_PG_PASSWORD": "x"})
	for _, key := range []string{"log", "persistence", "global", "services", "clusterMetadata", "dcRedirectionPolicy", "dynamicConfigClient"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("no %s", key)
		}
	}
}

// TestServerConfigNoFrontendHere: a node without a frontend reaches the cluster's first one.
func TestServerConfigNoFrontendHere(t *testing.T) {
	in := lab()
	in.Node, in.Address, in.Frontend = "vps-04", "10.80.0.14", "10.80.0.11:7233"
	got, err := serverConfig(in, role(t, in, "history"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("rpcAddress: 10.80.0.11:7233 }")) || !bytes.Contains(got, []byte(`broadcastAddress: "10.80.0.14"`)) {
		t.Errorf("history.yaml on vps-04:\n%s", got)
	}
}

func TestServerConfigTLS(t *testing.T) {
	in := lab()
	in.Postgres.Host, in.Postgres.TLS = "db.example.com", true
	got, err := serverConfig(in, role(t, in, "history"))
	if err != nil {
		t.Fatal(err)
	}
	tls := `        tls: { enabled: true, enableHostVerification: true, serverName: "db.example.com" }`
	if n := strings.Count(string(got), tls); n != 2 {
		t.Errorf("%d tls blocks, want one per store:\n%s", n, got)
	}
	if bytes.Contains(lab().mustRender(t, "history"), []byte("tls:")) {
		t.Error("tls: false still renders a tls block")
	}
}

func (in NodeInput) mustRender(t *testing.T, name string) []byte {
	t.Helper()
	b, err := serverConfig(in, role(t, in, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestServerConfigByteStable: the same input renders the same bytes, every time.
func TestServerConfigByteStable(t *testing.T) {
	first := lab().mustRender(t, "history")
	for range 20 {
		if !bytes.Equal(lab().mustRender(t, "history"), first) {
			t.Fatal("two renders of the same input differ")
		}
	}
}

// TestPortScheme: each role's gRPC port decides the last digit of every port it owns.
func TestPortScheme(t *testing.T) {
	for _, r := range spec.Roles {
		p := PortsOf(r)
		digit := p.GRPC % 10
		for name, port := range map[string]int{"membership": p.Membership, "metrics": p.Metrics, "pprof": p.Pprof} {
			if port%10 != digit {
				t.Errorf("%s %s port %d does not end in %d", r, name, port, digit)
			}
		}
		if p.Role != r {
			t.Errorf("PortsOf(%s).Role = %q", r, p.Role)
		}
	}
	if PortsOf("frontend").HTTP != 7243 || PortsOf("history").HTTP != 0 {
		t.Error("only frontend serves HTTP, on 7243")
	}
}

func TestServerConfigUnknownField(t *testing.T) {
	// missingkey=error: a template naming a field the data lacks fails instead of writing "<no value>".
	if _, err := execute("server.yaml", map[string]any{}); err == nil {
		t.Error("rendering with no data succeeded")
	}
}
