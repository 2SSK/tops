package cli

import (
	"fmt"
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/remote"
	"github.com/2SSK/tops/internal/term"
)

// Version is the tops version reported by tops version and in every --json
// document. make build and GoReleaser set it with -ldflags -X; go install cannot,
// so init falls back to the module version it records. Other builds say dev.
var Version = "dev"

func init() {
	bi, ok := debug.ReadBuildInfo()
	Version = moduleVersion(Version, bi, ok)
}

// moduleVersion is the version a build should report: the stamped  one if -X set it, else
// the one Go recorded, unless that is the empty or devel of a test binary or plain build.
func moduleVersion(stamped string, bi *debug.BuildInfo, ok bool) string {
	if stamped != "dev" || !ok || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return stamped
	}
	return bi.Main.Version
}

// buildInfo is what tops version reports about this binary
type buildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"goVersion"`
}

// versionDocument is the --json result of tops version.
type versionDocument struct {
	Kind        string        `json:"kind"`
	TopsVersion string        `json:"topsVersion"`
	Env         string        `json:"env,omitempty"`
	Cluster     string        `json:"cluster,omitempty"`
	Tops        buildInfo     `json:"tops"`
	Nodes       []nodeVersion `json:"nodes,omitempty"`
}

// nodeVersion is the helper one node runs: always this build, since connecting uploads it.
type nodeVersion struct {
	Name         string `json:"name"`
	HelperSHA256 string `json:"helperSha256"` // first 16 hex digits: the directory on the node
	Uploaded     bool   `json:"uploaded"`     // by this run
}

func nodeVersions(nodes []*remote.Node) []nodeVersion {
	vs := make([]nodeVersion, len(nodes))
	for i, n := range nodes {
		vs[i] = nodeVersion{Name: n.Node, HelperSHA256: n.SHA256[:16], Uploaded: n.Uploaded}
	}
	return vs
}

// writeNodeVersions prints "vps-01 helper c1d975b4e54a0326 uploaded", names aligned.
func writeNodeVersions(w io.Writer, vs []nodeVersion) error {
	width := 0
	for _, v := range vs {
		width = max(width, len(v.Name))
	}
	for _, v := range vs {
		state := "present"
		if v.Uploaded {
			state = "uploaded"
		}
		if _, err := fmt.Fprintf(w, "%-*s  helper %s  %s\n", width, v.Name, v.HelperSHA256, state); err != nil {
			return err
		}
	}
	return nil
}

// newBuildInfo reads the commit and Go version from bi. go run and go test
// binaries carry no commit, so Commit stays empty.
func newBuildInfo(bi *debug.BuildInfo, ok bool) buildInfo {
	info := buildInfo{Version: Version}
	if !ok {
		return info
	}
	info.GoVersion = bi.GoVersion

	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" {
			info.Commit = s.Value
		}
	}

	return info
}

// String formats info as "tops <version> (<commit>, <go version>)".
func (info buildInfo) String() string {
	commit := "unknown"
	if info.Commit != "" {
		n := min(7, len(info.Commit))
		commit = info.Commit[:n]
	}
	return fmt.Sprintf("tops %s (%s, %s)", info.Version, commit, info.GoVersion)
}

func newVersionCommand(opts *Options) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the tops version",
		GroupID: "integrate",
		Args:    usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			doc := versionDocument{Kind: "Version", TopsVersion: Version, Tops: newBuildInfo(debug.ReadBuildInfo())}
			if opts.Env != "" { // with -e: connect, which uploads the helper where it is missing
				env, err := loadEnv(cmd, opts)
				if err != nil {
					return err
				}
				c, err := connect(cmd.Context(), opts, env)
				if err != nil {
					return err
				}
				defer c.Close()
				doc.Env, doc.Cluster, doc.Nodes = env.Name, env.Cluster.Cluster, nodeVersions(c.nodes)
			}
			if opts.JSON {
				return term.WriteJSON(cmd.OutOrStdout(), doc)
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), doc.Tops); err != nil {
				return err
			}
			return writeNodeVersions(cmd.OutOrStdout(), doc.Nodes)
		},
	}
}
