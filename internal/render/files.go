package render

import "io/fs"

const mib = 1 << 20

// Effect is what installing a changed file does to the node's roles (backend schema "Files on
// a node"). apply groups a node's changed files by it: one restart per role, however many of
// its files changed.
type Effect string

// The effects of the files rendered so far.
const (
	RestartRole Effect = "restart role"      // the file's role restarts
	RestartAll  Effect = "restart all roles" // every role on the node restarts
)

// File is one rendered file and how tops installs it
type File struct {
	Path    string
	Content []byte
	Mode    fs.FileMode
	Owner   string
	Group   string
	Role    string // "" for a file every role on the node shares
	Effect  Effect
}

// FileSet is every file tops installs on a node: the shared files first, then each role's in
// stage order.
type FileSet []File

// roleData is what a role's env and limits files see: its sizes, limits in MiB.
type roleData struct {
	NodeInput
	Role                              string
	MemoryHigh, MemoryMax, GoMemLimit int64 // MiB
	CPUWeight                         int
}

// Render renders every file of a node.
func Render(in NodeInput) (FileSet, error) {
	todo := []fromTemplate{
		{File{Path: "/etc/systemd/system/temporal@.service", Mode: 0o644, Owner: "root", Group: "root", Effect: RestartAll}, "temporal@.service", nil},
		{File{Path: "/etc/temporal/temporal.env", Mode: 0o600, Owner: "root", Group: "root", Effect: RestartAll}, "temporal.env", in},
	}
	for _, r := range in.Roles {
		todo = append(todo, roleFiles(in, r)...)
	}
	files := make(FileSet, len(todo))
	for i, t := range todo {
		content, err := execute(t.template, t.data)
		if err != nil {
			return nil, err
		}
		files[i] = t.File
		files[i].Content = content
	}
	return files, nil
}

// fromTemplate is a file still to render: everything but its content, and what renders it.
type fromTemplate struct {
	File
	template string
	data     any
}

// roleFiles lists one role's files: its server config, environment and systemd limits.
func roleFiles(in NodeInput, r RoleInput) []fromTemplate {
	d := roleData{
		NodeInput:  in,
		Role:       r.Role,
		MemoryHigh: r.Size.MemoryHigh / mib,
		MemoryMax:  r.Size.MemoryMax / mib,
		GoMemLimit: r.Size.GoMemLimit / mib,
		CPUWeight:  r.Size.CPUWeight,
	}
	return []fromTemplate{
		{File{Path: "/etc/temporal/" + r.Role + ".yaml", Mode: 0o640, Owner: "root", Group: "temporal", Role: r.Role, Effect: RestartRole}, "server.yaml", newServerData(in, r)},
		{File{Path: "/etc/temporal/" + r.Role + ".env", Mode: 0o640, Owner: "root", Group: "temporal", Role: r.Role, Effect: RestartRole}, "role.env", d},
		{File{Path: "/etc/systemd/system/temporal@" + r.Role + ".service.d/limits.conf", Mode: 0o644, Owner: "root", Group: "root", Role: r.Role, Effect: RestartRole}, "limits.conf", d},
	}
}
