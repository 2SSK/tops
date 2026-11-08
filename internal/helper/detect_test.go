package helper

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// 16 GiB, as /proc/meminfo writes it.
const meminfo = "MemTotal:       16777216 kB\nMemFree:         1048576 kB\n"

const gib = 1 << 30

// fakeHost writes files under a temporary directory and returns a Host that reads them.
// Paths start with proc/ or cgroup/.
func fakeHost(t *testing.T, files map[string]string) Host {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Host{Proc: filepath.Join(root, "proc"), Cgroup: filepath.Join(root, "cgroup")}
}

func TestMemory(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  wire.Detected
	}{
		{
			name: "VM: PID 1 in init.scope, no limit anywhere",
			files: map[string]string{
				"proc/meminfo":                 meminfo,
				"proc/1/cgroup":                "0::/init.scope\n",
				"cgroup/init.scope/memory.max": "max\n",
			},
			want: wire.Detected{Value: 16 * gib, Source: "meminfo", Machine: 16 * gib},
		},
		{
			name: "container: the limit is on the namespace root",
			files: map[string]string{
				"proc/meminfo":      meminfo,
				"proc/1/cgroup":     "0::/\n",
				"cgroup/memory.max": "1342177280\n",
			},
			want: wire.Detected{Value: 1342177280, Source: "cgroup:/sys/fs/cgroup/memory.max", Machine: 16 * gib},
		},
		{
			name: "an ancestor is lower than PID 1's own cgroup",
			files: map[string]string{
				"proc/meminfo":          meminfo,
				"proc/1/cgroup":         "0::/a/b\n",
				"cgroup/a/memory.max":   "1073741824\n",
				"cgroup/a/b/memory.max": "2147483648\n",
			},
			want: wire.Detected{Value: gib, Source: "cgroup:/sys/fs/cgroup/a/memory.max", Machine: 16 * gib},
		},
		{
			name: "PID 1's own cgroup is the lowest",
			files: map[string]string{
				"proc/meminfo":          meminfo,
				"proc/1/cgroup":         "0::/a/b\n",
				"cgroup/a/memory.max":   "max\n",
				"cgroup/a/b/memory.max": "536870912\n",
			},
			want: wire.Detected{Value: 512 << 20, Source: "cgroup:/sys/fs/cgroup/a/b/memory.max", Machine: 16 * gib},
		},
		{
			name: "a limit above MemTotal does not count",
			files: map[string]string{
				"proc/meminfo":      meminfo,
				"proc/1/cgroup":     "0::/\n",
				"cgroup/memory.max": "68719476736\n",
			},
			want: wire.Detected{Value: 16 * gib, Source: "meminfo", Machine: 16 * gib},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fakeHost(t, tt.files).Memory()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestMemoryErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"no meminfo", map[string]string{"proc/1/cgroup": "0::/\n"}},
		{"no MemTotal", map[string]string{"proc/meminfo": "MemFree: 1 kB\n", "proc/1/cgroup": "0::/\n"}},
		{"MemTotal not a number", map[string]string{"proc/meminfo": "MemTotal: lots kB\n", "proc/1/cgroup": "0::/\n"}},
		{"no PID 1 cgroup", map[string]string{"proc/meminfo": meminfo}},
		{"cgroup v1 only", map[string]string{"proc/meminfo": meminfo, "proc/1/cgroup": "4:memory:/\n"}},
		{"memory.max not a number", map[string]string{"proc/meminfo": meminfo, "proc/1/cgroup": "0::/\n", "cgroup/memory.max": "lots\n"}},
		{"memory.max unreadable", map[string]string{"proc/meminfo": meminfo, "proc/1/cgroup": "0::/\n", "cgroup/memory.max/x": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := fakeHost(t, tt.files).Memory(); err == nil {
				t.Errorf("got %+v, want an error", got)
			}
		})
	}
}

func TestCgroupDirs(t *testing.T) {
	tests := []struct {
		cgroup string
		want   []string
	}{
		{"0::/\n", []string{"/"}},
		{"0::/init.scope\n", []string{"/init.scope", "/"}},
		{"0::/a/b\n", []string{"/a/b", "/a", "/"}},
		{"12:pids:/x\n0::/a\n", []string{"/a", "/"}},
	}
	for _, tt := range tests {
		t.Run(tt.cgroup, func(t *testing.T) {
			got, err := fakeHost(t, map[string]string{"proc/1/cgroup": tt.cgroup}).cgroupDirs()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
