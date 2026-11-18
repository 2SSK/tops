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

// serverData is what server.yaml sees: the node, the role being rendered, its ports, and every
// role's ports for the services block.
type serverData struct {
	NodeInput
	Role         RoleInput
	Ports        Ports
	All          []Ports
	PostgresAddr string
}

// newServerData is what /etc/temporal/<role>.yaml is rendered from, for one role on the node.
func newServerData(in NodeInput, r RoleInput) serverData {
	return serverData{
		NodeInput:    in,
		Role:         r,
		Ports:        PortsOf(r.Role),
		All:          allPorts(),
		PostgresAddr: net.JoinHostPort(in.Postgres.Host, strconv.Itoa(in.Postgres.Port)),
	}
}

func execute(name string, data any) ([]byte, error) {
	var b bytes.Buffer
	if err := templates.ExecuteTemplate(&b, name, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", name, err)
	}
	return b.Bytes(), nil
}
