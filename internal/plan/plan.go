// Package plan compares what tops would install on a node with what is there (TRD "Plan and
// apply"). It is pure: the facts come in, a NodePlan goes out, nothing is touched.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/wire"
	"github.com/aymanbagabas/go-udiff"
)

// Action is what apply would do to a file.
type Action string

// File actions.
const (
	Create Action = "create"
	Update Action = "update"
	Remove Action = "remove"
)

// RoleAction is what apply would do to a role's unit.
type RoleAction string

// Role actions.
const (
	Keep  RoleAction = "keep"
	Start RoleAction = "start"
	Stop  RoleAction = "stop"
)

// FileChange is one file apply would write or remove (backend schema "NodePlan").
type FileChange struct {
	Path   string        `json:"path"`
	Role   string        `json:"role"` // "" for a shared or unknown file
	Action Action        `json:"action"`
	Effect render.Effect `json:"effect"`
	Diff   string        `json:"diff,omitempty"` // unified, secrets as ***; updates only
	Drift  bool          `json:"drift"`          // the live file is not what the last apply wrote
}

// RoleChange is one role on the node: started, stopped, or kept and maybe restarted.
type RoleChange struct {
	Role    string     `json:"role"`
	Action  RoleAction `json:"action"`
	Restart bool       `json:"restart"` // kept, and one of its files changes
}

// Validated says whether the target Temporal checked the rendered files on this node.
type Validated struct {
	OK      bool     `json:"ok"`
	Release string   `json:"release"`
	Checks  []string `json:"checks"`
}

// NodePlan is everything apply would change on one node.
type NodePlan struct {
	Name      string       `json:"name"`
	Reachable bool         `json:"reachable"`
	Roles     []RoleChange `json:"roles"`
	Files     []FileChange `json:"files"`
	Validated Validated    `json:"validated"`
}

// Changes reports whether apply would change anything on the node.
func (p NodePlan) Changes() bool {
	return len(p.Files) > 0 || slices.ContainsFunc(p.Roles, func(r RoleChange) bool { return r.Action != Keep || r.Restart })
}

// ownedDirs are the directories tops owns entirely: a file there that tops did not render is
// removed (backend schema "Files on a node").
var ownedDirs = []string{"/etc/temporal"}

// Want is what plan asks a node's facts for: every file tops would write, by its sha256.
func Want(files render.FileSet) wire.FactsArgs {
	want := make(map[string]string, len(files))
	for _, f := range files {
		want[f.Path] = sha(f.Content)
	}
	return wire.FactsArgs{Want: want, Dirs: ownedDirs}
}

// Validate is what the node's helper checks with the target release: each role's server config
// and dynamic config, with the environment systemd gives that role, the shared file first.
func Validate(version string, files render.FileSet) wire.Validate {
	byPath := map[string]render.File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	v := wire.Validate{Version: version, Roles: []wire.RoleFiles{}}
	for _, f := range files {
		if f.Path != render.ConfigPath(f.Role) {
			continue // one entry per role, in the files' order
		}
		r := wire.RoleFiles{Role: f.Role, Config: string(f.Content), DynamicConfig: string(byPath[render.DynamicConfigPath(f.Role)].Content), Env: []string{}, Secrets: []string{}}
		for _, env := range []render.File{byPath[render.SharedEnvPath], byPath[render.EnvPath(f.Role)]} {
			for line := range strings.Lines(string(env.Content)) {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				r.Env = append(r.Env, line)
				if env.Secret {
					key, _, _ := strings.Cut(line, "=")
					r.Secrets = append(r.Secrets, key)
				}
			}
		}
		v.Roles = append(v.Roles, r)
	}
	return v
}

// Node plans one node: files is what tops would install, facts what is there, version the
// spec's Temporal version, and secrets the values no diff may show.
func Node(name, version string, files render.FileSet, facts wire.Facts, secrets []string) NodePlan {
	p := NodePlan{Name: name, Reachable: true, Roles: []RoleChange{}, Files: []FileChange{}, Validated: validated(version, facts.Releases)}
	for _, f := range files {
		if c, ok := fileChange(f, facts, secrets); ok {
			p.Files = append(p.Files, c)
		}
	}
	for _, path := range facts.Unknown {
		p.Files = append(p.Files, FileChange{Path: path, Action: Remove})
	}
	p.Roles = roleChanges(files, facts.Applied, p.Files)
	return p
}

// fileChange compares one file with the node's copy. Content, mode and owner all count.
func fileChange(f render.File, facts wire.Facts, secrets []string) (FileChange, bool) {
	live := facts.Files[f.Path]
	c := FileChange{Path: f.Path, Role: f.Role, Effect: f.Effect, Drift: drifted(f.Path, live, facts.Applied)}
	switch {
	case !live.Exists:
		c.Action = Create
	case live.SHA256 != sha(f.Content) && f.Secret:
		c.Action = Update
		c.Diff = secretDiff(f.Path, string(live.Content), string(f.Content))
	case live.SHA256 != sha(f.Content):
		c.Action = Update
		c.Diff = udiff.Unified(f.Path, f.Path, mask(string(live.Content), secrets), mask(string(f.Content), secrets))
	case live.Mode != mode(f) || live.Owner != f.Owner || live.Group != f.Group:
		c.Action = Update
	default:
		return FileChange{}, false
	}
	return c, true
}

// drifted reports a file that is not what the last apply wrote: edited by hand or deleted.
func drifted(path string, live wire.FileState, applied *wire.Applied) bool {
	if applied == nil {
		return false
	}
	was, ok := applied.Files[path]
	return ok && (!live.Exists || live.SHA256 != was.SHA256)
}

// roleChanges starts the roles tops has not enabled here, stops the ones the spec dropped,
// and restarts a kept role when one of its files, or a shared file, changes.
func roleChanges(files render.FileSet, applied *wire.Applied, changes []FileChange) []RoleChange {
	var enabled []string
	if applied != nil {
		enabled = applied.Roles
	}
	roles := []RoleChange{}
	var wanted []string
	for _, f := range files {
		if f.Role == "" || slices.Contains(wanted, f.Role) {
			continue
		}
		wanted = append(wanted, f.Role)
		if !slices.Contains(enabled, f.Role) {
			roles = append(roles, RoleChange{Role: f.Role, Action: Start})
			continue
		}
		restart := slices.ContainsFunc(changes, func(c FileChange) bool {
			return c.Role == f.Role || c.Effect == render.RestartAll
		})
		roles = append(roles, RoleChange{Role: f.Role, Action: Keep, Restart: restart})
	}
	for _, r := range enabled {
		if !slices.Contains(wanted, r) {
			roles = append(roles, RoleChange{Role: r, Action: Stop})
		}
	}
	return roles
}

// validated is whether the spec's release checked the files. Until bootstrap stages a
// release (T3.1) there is nothing to check them with, and plan says so.
func validated(version string, releases []string) Validated {
	v := Validated{Release: version, Checks: []string{}}
	if !slices.Contains(releases, version) {
		v.Checks = append(v.Checks, "not validated: "+version+" not staged")
	}
	return v
}

// secretDiff diffs a file of KEY=VALUE secrets by key: every value is ***, and a value that
// changes is *** (changed). The old values are the node's, which tops does not know, so
// masking by value could not hide them.
func secretDiff(path, live, want string) string {
	was := map[string]string{}
	var before, after strings.Builder
	for line := range strings.Lines(live) {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		if !ok {
			before.WriteString(line)
			continue
		}
		was[key] = value
		before.WriteString(key + "=***\n")
	}
	for line := range strings.Lines(want) {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		if !ok {
			after.WriteString(line)
			continue
		}
		if old, had := was[key]; had && old != value {
			after.WriteString(key + "=*** (changed)\n")
			continue
		}
		after.WriteString(key + "=***\n")
	}
	return udiff.Unified(path, path, before.String(), after.String())
}

// mask replaces every secret value with ***, so no diff, on screen or in JSON, shows one.
func mask(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mode is a file's mode as facts reports it: 0640.
func mode(f render.File) string {
	return fmt.Sprintf("%04o", f.Mode.Perm())
}
