package helper

import (
	"errors"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// A 12-CPU machine, as /proc/stat lists it: the "cpu" total line, then one line per CPU.
const stat = "cpu  1 2 3\ncpu0 1 2 3\ncpu1 1 2 3\ncpu2 1 2 3\ncpu3 1 2 3\ncpu4 1 2 3\ncpu5 1 2 3\n" +
	"cpu6 1 2 3\ncpu7 1 2 3\ncpu8 1 2 3\ncpu9 1 2 3\ncpu10 1 2 3\ncpu11 1 2 3\nintr 5\nctxt 9\n"

// withAffinity sets a fake sched_getaffinity on h.
func withAffinity(h Host, n int) Host {
	h.Affinity = func() (int, error) { return n, nil }
	return h
}

func TestCores(t *testing.T) {
	tests := []struct {
		name     string
		affinity int
		files    map[string]string
		want     wire.Detected
	}{
		{
			name:     "VM: no limit, every CPU",
			affinity: 12,
			files: map[string]string{
				"proc/stat":                    stat,
				"proc/1/cgroup":                "0::/init.scope\n",
				"cgroup/cpuset.cpus.effective": "0-11\n",
				"cgroup/init.scope/cpu.max":    "max 100000\n",
			},
			want: wire.Detected{Value: 12, Source: "affinity", Machine: 12},
		},
		{
			name:     "VPSLAB: 1.5 CPUs on the namespace root, PID 1 in init.scope",
			affinity: 12,
			files: map[string]string{
				"proc/stat":                    stat,
				"proc/1/cgroup":                "0::/init.scope\n",
				"cgroup/cpu.max":               "150000 100000\n",
				"cgroup/cpuset.cpus.effective": "0-11\n",
			},
			want: wire.Detected{Value: 1.5, Source: "cgroup:/sys/fs/cgroup/cpu.max", Machine: 12},
		},
		{
			name:     "affinity limits PID 1 to fewer CPUs",
			affinity: 4,
			files: map[string]string{
				"proc/stat":     stat,
				"proc/1/cgroup": "0::/\n",
			},
			want: wire.Detected{Value: 4, Source: "affinity", Machine: 12},
		},
		{
			name:     "a cpuset of 0-1,4",
			affinity: 12,
			files: map[string]string{
				"proc/stat":                    stat,
				"proc/1/cgroup":                "0::/\n",
				"cgroup/cpuset.cpus.effective": "0-1,4\n",
			},
			want: wire.Detected{Value: 3, Source: "cgroup:/sys/fs/cgroup/cpuset.cpus.effective", Machine: 12},
		},
		{
			name:     "the lowest quota on the path wins",
			affinity: 12,
			files: map[string]string{
				"proc/stat":          stat,
				"proc/1/cgroup":      "0::/a/b\n",
				"cgroup/a/cpu.max":   "50000 100000\n",
				"cgroup/a/b/cpu.max": "200000 100000\n",
				"cgroup/cpu.max":     "max 100000\n",
			},
			want: wire.Detected{Value: 0.5, Source: "cgroup:/sys/fs/cgroup/a/cpu.max", Machine: 12},
		},
		{
			name:     "a quota above the CPUs does not count",
			affinity: 12,
			files: map[string]string{
				"proc/stat":      stat,
				"proc/1/cgroup":  "0::/\n",
				"cgroup/cpu.max": "1600000 100000\n",
			},
			want: wire.Detected{Value: 12, Source: "affinity", Machine: 12},
		},
		{
			name:     "an empty cpuset is no limit",
			affinity: 12,
			files: map[string]string{
				"proc/stat":                    stat,
				"proc/1/cgroup":                "0::/\n",
				"cgroup/cpuset.cpus.effective": "\n",
			},
			want: wire.Detected{Value: 12, Source: "affinity", Machine: 12},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := withAffinity(fakeHost(t, tt.files), tt.affinity).Cores()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCoresErrors(t *testing.T) {
	ok := map[string]string{"proc/stat": stat, "proc/1/cgroup": "0::/\n"}
	with := func(name, content string) map[string]string {
		files := map[string]string{name: content}
		for k, v := range ok {
			if k != name {
				files[k] = v
			}
		}
		return files
	}
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"no stat", map[string]string{"proc/1/cgroup": "0::/\n"}},
		{"stat lists no CPUs", with("proc/stat", "cpu  1 2 3\nintr 5\n")},
		{"no PID 1 cgroup", map[string]string{"proc/stat": stat}},
		{"cpu.max one field", with("cgroup/cpu.max", "150000\n")},
		{"cpu.max quota not a number", with("cgroup/cpu.max", "lots 100000\n")},
		{"cpu.max period not a number", with("cgroup/cpu.max", "150000 lots\n")},
		{"cpu.max period zero", with("cgroup/cpu.max", "150000 0\n")},
		{"cpuset not a number", with("cgroup/cpuset.cpus.effective", "a-b\n")},
		{"cpuset range backwards", with("cgroup/cpuset.cpus.effective", "3-1\n")},
		{"cpu.max unreadable", with("cgroup/cpu.max/x", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := withAffinity(fakeHost(t, tt.files), 12).Cores(); err == nil {
				t.Errorf("got %+v, want an error", got)
			}
		})
	}

	t.Run("affinity fails", func(t *testing.T) {
		h := fakeHost(t, ok)
		h.Affinity = func() (int, error) { return 0, errors.New("EPERM") }
		if got, err := h.Cores(); err == nil {
			t.Errorf("got %+v, want an error", got)
		}
	})
}

// The real machine: whatever it is, the effective values are positive and at most the
// machine's.
func TestSystem(t *testing.T) {
	for name, detect := range map[string]func() (wire.Detected, error){
		"memory": System.Memory,
		"cores":  System.Cores,
	} {
		t.Run(name, func(t *testing.T) {
			d, err := detect()
			if err != nil {
				t.Fatal(err)
			}
			if d.Value <= 0 || d.Value > d.Machine || d.Source == "" {
				t.Errorf("implausible: %+v", d)
			}
			t.Logf("%s: %+v", name, d)
		})
	}
}
