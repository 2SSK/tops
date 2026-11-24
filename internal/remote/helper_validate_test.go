//go:build helper

package remote

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/2SSK/tops/internal/plan"
	"github.com/2SSK/tops/internal/release"
	"github.com/2SSK/tops/internal/render"
	"github.com/2SSK/tops/internal/wire"
)

// temporalVersion is the release the goldens are validated with: the spec fixture's.
const temporalVersion = "1.31.3"

// serverRelease is Temporal's real server release for this machine's arch, checked against
// its checksums.txt, from .cache/releases when an earlier run fetched it.
func serverRelease(t *testing.T) (wire.Download, []byte) {
	t.Helper()
	f := release.Fetcher{HTTP: http.DefaultClient, Base: release.GitHub}
	d, err := f.Resolve(t.Context(), release.Server, temporalVersion, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join("..", "..", ".cache", "releases", path.Base(d.URL))
	b, err := os.ReadFile(cache) //nolint:gosec // our own cache
	if err != nil || hexSum(b) != d.SHA256 {
		if b, err = f.Get(t.Context(), d.URL, 512<<20); err != nil {
			t.Fatal(err)
		}
		if hexSum(b) != d.SHA256 {
			t.Fatalf("%s does not match checksums.txt", d.URL)
		}
		if err := os.MkdirAll(filepath.Dir(cache), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cache, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return d, b
}

// goldenHeader is the line before each file in internal/render's goldens.
var goldenHeader = regexp.MustCompile(`^==> (\S+) \(\d+ \S+:\S+, role "([a-z]*)", .+\) <==$`)

// goldenFiles reads one node's render golden back into its files: the exact bytes tops
// installs, which T3.2 says the release must accept.
func goldenFiles(t *testing.T, node string) render.FileSet {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "render", "testdata", "lab-"+node+".txt")) //nolint:gosec // the render package's goldens
	if err != nil {
		t.Fatal(err)
	}
	var files render.FileSet
	var content bytes.Buffer
	flush := func() {
		if len(files) > 0 { // each file is followed by one blank line
			files[len(files)-1].Content = bytes.Clone(bytes.TrimSuffix(content.Bytes(), []byte("\n")))
		}
		content.Reset()
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if m := goldenHeader.FindStringSubmatch(sc.Text()); m != nil {
			flush()
			files = append(files, render.File{Path: m[1], Role: m[2], Secret: m[1] == render.SharedEnvPath})
			continue
		}
		content.WriteString(sc.Text() + "\n")
	}
	flush()
	if len(files) == 0 {
		t.Fatalf("no files in the %s golden", node)
	}
	return files
}

// stagedNode is a fresh container with the real 1.31.3 server release staged.
func stagedNode(t *testing.T) (*container, *Node) {
	t.Helper()
	d, archive := serverRelease(t)
	c := startContainer(t)
	n, err := c.connect(t, c.dialer(t, c.cfg))
	if err != nil {
		t.Fatal(err)
	}
	base := serveOnNode(t, n, &releaseServer{files: map[string][]byte{"/" + path.Base(d.URL): archive}})
	d.URL = base + "/" + path.Base(d.URL)
	holdLease(t, n)
	if _, err := n.Stage(t.Context(), wire.Stage{User: true, Releases: []wire.Download{d}}); err != nil {
		t.Fatal(err)
	}
	return c, n
}

// TestHelperValidateGoldens is T3.2's done-when: every role's golden render validates with
// the real temporal-server 1.31.3, the unit and drop-ins with the node's systemd, and
// /run/tops is empty afterwards.
func TestHelperValidateGoldens(t *testing.T) {
	c, n := stagedNode(t)
	for _, node := range []string{"vps-01", "vps-02", "vps-03"} {
		v := plan.Validate(temporalVersion, goldenFiles(t, node))
		if len(v.Roles) == 0 {
			t.Fatalf("%s: no roles in the golden", node)
		}
		r, err := n.Validate(t.Context(), v)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Rejected) != 0 {
			t.Errorf("%s: temporal-server %s rejected the goldens: %+v", node, temporalVersion, r.Rejected)
		}
	}
	if got := docker(t, nil, "exec", c.id, "ls", "-A", "/run/tops"); got != "" {
		t.Errorf("/run/tops holds %q after validate", got)
	}
}

// TestHelperValidateRejects: for each role, a config and a dynamic config the real release
// refuses, and a drop-in systemd refuses, and a unit it refuses. The config puts the password
// where a number goes, so Temporal's own message quotes it, shortened by yaml.v3 to its first
// 7 characters: the rejection must say *** instead. systemd's say where each file goes, once.
func TestHelperValidateRejects(t *testing.T) {
	c, n := stagedNode(t)
	v := plan.Validate(temporalVersion, goldenFiles(t, "vps-01"))
	want, wantMessage := breakFiles(t, &v)
	r, err := n.Validate(t.Context(), v)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, rej := range r.Rejected {
		got = append(got, rej.Role+" "+rej.File)
		if m, ok := wantMessage[rej.Role+" "+rej.File]; ok && rej.Message != m {
			t.Errorf("%s %s: message %q, want %q", rej.Role, rej.File, rej.Message, m)
		}
		if strings.Contains(rej.Message, "lab-pas") {
			t.Errorf("%s %s: the message shows the password: %s", rej.Role, rej.File, rej.Message)
		}
		if rej.File == "config" && !strings.HasPrefix(rej.Message, "Unable to load configuration: yaml: unmarshal errors: line 5: cannot unmarshal !!str `***` into int32") {
			t.Errorf("%s config: message %q, want Temporal's whole error", rej.Role, rej.Message)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("rejected %q, want %q", got, want)
	}
	if got := docker(t, nil, "exec", c.id, "ls", "-A", "/run/tops"); got != "" {
		t.Errorf("/run/tops holds %q after validate", got)
	}
}

// breakFiles breaks every file of v once and returns the rejections that follow, as
// "<role> <file>", in the helper's order, and systemd's exact message for its files.
func breakFiles(t *testing.T, v *wire.Validate) ([]string, map[string]string) {
	t.Helper()
	if !strings.Contains(v.Unit, "\nRestart=on-failure\n") {
		t.Fatal("temporal@.service has no Restart=on-failure to break")
	}
	v.Unit = strings.Replace(v.Unit, "\nRestart=on-failure\n", "\nRestart=sometimes\n", 1)
	wantMessage := map[string]string{
		" unit": fmt.Sprintf("/etc/systemd/system/temporal@.service:%d: Failed to parse service restart specifier, ignoring: sometimes", lineOf(v.Unit, "Restart=sometimes")),
	}
	var want, limits []string
	for i := range v.Roles {
		r := &v.Roles[i]
		if !strings.Contains(r.Config, "numHistoryShards: 512") {
			t.Fatalf("%s.yaml has no numHistoryShards: 512 to break", r.Role)
		}
		r.Config = strings.Replace(r.Config, "numHistoryShards: 512", `numHistoryShards: {{ env "TOPS_PG_PASSWORD" }}`, 1)
		r.DynamicConfig += "tops.notAKey:\n  - value: 1\n"
		r.Limits += "MemoryMax=lots\n"
		want = append(want, r.Role+" config", r.Role+" dynamicConfig")
		limits = append(limits, r.Role+" limits")
		wantMessage[r.Role+" limits"] = fmt.Sprintf("/etc/systemd/system/temporal@%s.service.d/limits.conf:%d: Invalid memory limit 'lots', ignoring: Invalid argument", r.Role, lineOf(r.Limits, "MemoryMax=lots"))
	}
	return append(append(want, " unit"), limits...), wantMessage
}

// lineOf is the number of the line of s that is line, from 1, as systemd counts.
func lineOf(s, line string) int {
	for i, l := range strings.Split(s, "\n") {
		if l == line {
			return i + 1
		}
	}
	return 0
}
