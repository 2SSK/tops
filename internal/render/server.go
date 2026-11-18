package render

import (
	"bytes"
	"embed"
	"fmt"
	"net"
	"strconv"
	"text/template"
)

//go:embed templates
var templateFS embed.FS

// templates use [[ ]] so Temporal's own {{ env "X" }} passes through untouched.
var templates = template.Must(template.New("").Delims("[[", "]]").Option("missingkey=error").ParseFS(templateFS, "templates/*"))

// serverData is what server.yaml ses: the node, the role being rendered, its ports, and every
// role's ports for the services block.
type serverData struct {
	NodeInput
	Role         RoleInput
	Ports        Ports
	All          []Ports
	PostgresAddr string
}

// serverConfig renders /etc/temporal/<role>.yaml for one role on the node.
func serverConfig(in NodeInput, r RoleInput) ([]byte, error) {
	return execute("server.yaml", serverData{
		NodeInput:    in,
		Role:         r,
		Ports:        PortsOf(r.Role),
		All:          allPorts(),
		PostgresAddr: net.JoinHostPort(in.Postgres.Host, strconv.Itoa(in.Postgres.Port)),
	})
}

func execute(name string, data any) ([]byte, error) {
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, name, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return b.Bytes(), nil
}
