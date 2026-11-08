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

// Host is where detection reads a machine: its /proc and its cgroup v2 mount.
type Host struct {
	Proc   string
	Cgroup string
}

// System is the machine the helper runs on.
var System = Host{Proc: "/proc", Cgroup: cgroupMount}

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
		limit, ok, err := readLimit(filepath.Join(h.Cgroup, dir, "memory.max"))
		if err != nil {
			return wire.Detected{}, err
		}
		if ok && limit < d.Value {
			d.Value = limit
			d.Source = "cgroup:" + path.Join(cgroupMount, dir, "memory.max")
		}
	}
	return d, nil
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

// readLimit reads a cgroup limit file. ok is false when there is no limit: the file is
// missing (the root cgroup has none) or says "max".
func readLimit(name string) (limit float64, ok bool, err error) {
	b, err := os.ReadFile(filepath.Clean(name))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read limit: %w", err)
	}
	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0, false, nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", name, err)
	}
	return float64(v), true, nil
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
