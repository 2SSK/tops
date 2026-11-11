// Package helper is the node side of tops: it runs as root on a node, reads one request
// and writes one response.
package helper

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/2SSK/tops/internal/wire"
)

// cgroupMount is where cgroup v2 is mounted on a node. Sources name files under it, even
// when a test reads them from somewhere else.
const cgroupMount = "/sys/fs/cgroup"

// Host is where detection reads a machine: its /proc, its cgroup v2 mount, and the
// number of CPUs PID 1 may run on.
type Host struct {
	Proc     string
	Cgroup   string
	Etc      string
	Affinity func() (int, error)
}

// System is the machine the helper runs on.
var System = Host{Proc: "/proc", Cgroup: cgroupMount, Etc: "/etc", Affinity: pid1Affinity}

// Memory returns the effective memory in bytes: MemTotal, lowered by the memory.max of
// PID 1's cgroup or of any ancestor visible from here.
func (h Host) Memory() (wire.Detected, error) {
	total, err := h.memTotal()
	if err != nil {
		return wire.Detected{}, err
	}
	dirs, err := h.cgroupDirs()
	if err != nil {
		return wire.Detected{}, err
	}

	d := wire.Detected{Value: total, Source: "meminfo", Machine: total}
	for _, dir := range dirs {
		if err := h.lower(&d, dir, "memory.max", readLimit); err != nil {
			return wire.Detected{}, err
		}
	}
	return d, nil
}

// Cores returns the effective cores: the CPUs PID 1 may run on, lowered by the
// cpuset.cpus.effective and the cpu.max quota of PID 1's cgroup or of any ancestor
// visible from here. A quota makes it fractional. "150000 100000" is 1.5.
func (h Host) Cores() (wire.Detected, error) {
	machine, err := h.onlineCPUs()
	if err != nil {
		return wire.Detected{}, err
	}
	n, err := h.Affinity()
	if err != nil {
		return wire.Detected{}, fmt.Errorf("CPU affinity: %w", err)
	}
	dirs, err := h.cgroupDirs()
	if err != nil {
		return wire.Detected{}, err
	}
	d := wire.Detected{Value: float64(n), Source: "affinity", Machine: machine}
	for _, dir := range dirs {
		if err := h.lower(&d, dir, "cpuset.cpus.effective", readCPUSet); err != nil {
			return wire.Detected{}, err
		}
		if err := h.lower(&d, dir, "cpu.max", readQuota); err != nil {
			return wire.Detected{}, err
		}
	}
	return d, nil
}

// lower reads one cgroup file with read, and lowers d to its value when that is below
// d's: the file then  becomes d's source.
func (h Host) lower(d *wire.Detected, dir, file string, read func(string) (float64, bool, error)) error {
	v, ok, err := read(filepath.Join(h.Cgroup, dir, file))
	if err != nil {
		return err
	}
	if ok && v < d.Value {
		d.Value = v
		d.Source = "cgroup:" + path.Join(cgroupMount, dir, file)
	}
	return nil
}

// cgroupDirs returns PID 1's cgroup and each of its ancestors, relative to the mount:
// "/a/b", "/a", "/".
func (h Host) cgroupDirs() ([]string, error) {
	b, err := os.ReadFile(filepath.Join(h.Proc, "1", "cgroup"))
	if err != nil {
		return nil, fmt.Errorf("read PID 1's cgroup: %w", err)
	}
	for line := range strings.Lines(string(b)) {
		// cgroup v2 is the line "0::<path>".
		p, ok := strings.CutPrefix(strings.TrimSpace(line), "0::")
		if !ok {
			continue
		}
		var dirs []string
		for d := path.Clean(p); ; d = path.Dir(d) {
			dirs = append(dirs, d)
			if d == "/" {
				return dirs, nil
			}
		}
	}
	return nil, errors.New("PID 1 is not in a cgroup v2 hierarchy")
}

// readFile returns a cgroup file's content without surrounding space. ok is false when
// the file is missing: the root cgroup has no limit files, and a controller may be off.
func readFile(name string) (content string, ok bool, err error) {
	b, err := os.ReadFile(filepath.Clean(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read cgroup file: %w", err)
	}
	return strings.TrimSpace(string(b)), true, nil
}

// readLimit reads memory.max. ok is false when there is no limit: no file, or "max".
func readLimit(name string) (limit float64, ok bool, err error) {
	s, ok, err := readFile(name)
	if !ok || err != nil || s == "max" {
		return 0, false, err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return float64(v), true, nil
}

func readQuota(name string) (cores float64, ok bool, err error) {
	s, ok, err := readFile(name)
	if !ok || err != nil {
		return 0, false, err
	}
	quota, period, found := strings.Cut(s, " ")
	if !found {
		return 0, false, fmt.Errorf("%s: want \"<quota> <period>\", got %q", name, s)
	}
	if quota == "max" {
		return 0, false, nil
	}
	q, err := strconv.ParseUint(quota, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	p, err := strconv.ParseUint(period, 10, 64)
	if err != nil || p == 0 {
		return 0, false, fmt.Errorf("%s: bad period %q", name, period)
	}
	return float64(q) / float64(p), true, nil
}

// readCPUSet reads cpuset.cpus.effective, a list like "0-3,6", as a count of CPUs. ok is
// false when the file is missing or empty.
func readCPUSet(name string) (cpus float64, ok bool, err error) {
	s, ok, err := readFile(name)
	if !ok || err != nil || s == "" {
		return 0, false, err
	}
	n := 0
	for part := range strings.SplitSeq(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		a, errA := strconv.Atoi(lo)
		b, errB := strconv.Atoi(hi)
		if errA != nil || errB != nil || b < a {
			return 0, false, fmt.Errorf("%s: bad CPU list %q", name, s)
		}
		n += b - a + 1
	}
	return float64(n), true, nil
}

// memTotal returns MemTotal from meminfo, in bytes.
func (h Host) memTotal() (float64, error) {
	b, err := os.ReadFile(filepath.Join(h.Proc, "meminfo"))
	if err != nil {
		return 0, fmt.Errorf("read meminfo: %w", err)
	}
	for line := range strings.Lines(string(b)) {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		kb, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("meminfo MemTotal: %w", err)
		}
		return float64(kb * 1024), nil
	}
	return 0, errors.New("meminfo has no MemTotal")
}

// onlineCPUs counts the machine's online CPUs: the "cpuN" lines of /proc/stat.
func (h Host) onlineCPUs() (float64, error) {
	b, err := os.ReadFile(filepath.Join(h.Proc, "stat"))
	if err != nil {
		return 0, fmt.Errorf("read stat: %w", err)
	}
	n := 0
	for line := range strings.Lines(string(b)) {
		if rest, ok := strings.CutPrefix(line, "cpu"); ok && rest != "" && rest[0] >= '0' && rest[0] <= '9' {
			n++
		}
	}
	if n == 0 {
		return 0, errors.New("stat lists no CPUs")
	}
	return float64(n), nil
}
