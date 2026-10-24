package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/2SSK/tops/internal/term"
)

// Version is the tops version reported by tops version and in every --json
// document.
// make build sets it with -ldflags -X; unstamped builds say dev.
var Version = "dev"

// buildInfo is what tops version reports about this binary
type buildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"goVersion"`
}

// versionDocument is the --json result of tops version.
type versionDocument struct {
	Kind        string    `json:"kind"`
	TopsVersion string    `json:"topsVersion"`
	Tops        buildInfo `json:"tops"`
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
			info := newBuildInfo(debug.ReadBuildInfo())
			if opts.JSON {
				return term.WriteJSON(cmd.OutOrStdout(), versionDocument{Kind: "Version", TopsVersion: Version, Tops: info})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), info)
			return err
		},
	}
}
