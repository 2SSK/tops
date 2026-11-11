package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/2SSK/tops/internal/wire"
)

// Hello describes the machine: who it is, what it runs, and its effective size.
// version is the helper's own, the same as the workstation's tops.
func (h Host) Hello(version string) (wire.Hello, error) {
	hostname, err := readLine(h.Proc, "sys/kernel/hostname")
	if err != nil {
		return wire.Hello{}, err
	}
	machineID, err := readLine(h.Etc, "machine-id")
	if err != nil {
		return wire.Hello{}, err
	}
	osName, err := h.osRelease()
	if err != nil {
		return wire.Hello{}, err
	}
	memory, err := h.Memory()
	if err != nil {
		return wire.Hello{}, err
	}
	cores, err := h.Cores()
	if err != nil {
		return wire.Hello{}, err
	}
	synced, err := h.Clock()
	if err != nil {
		return wire.Hello{}, fmt.Errorf("clock: %w", err)
	}

	return wire.Hello{
		HelperVersion: version,
		Hostname:      hostname,
		MachineID:     machineID,
		OS:            osName,
		Arch:          runtime.GOARCH,
		Memory:        memory,
		Cores:         cores,
		ClockSynced:   synced,
	}, nil
}

// readLine returns a one-line file under dir without surrounding space, and refuses an
// empty one.
func readLine(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(dir, name)))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", name, err)
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", fmt.Errorf("%s is empty", name)
	}
	return s, nil
}

// osRelease returns ID and VERSION_ID from os-release(5): "ubuntu 24.04". A rolling
// release has no VERSION_ID, and is its ID alone.
func (h Host) osRelease() (string, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(h.Etc, "os-release")))
	if err != nil {
		return "", fmt.Errorf("read os-release: %w", err)
	}
	fields := map[string]string{}
	for line := range strings.Lines(string(b)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			fields[k] = strings.Trim(v, `"'`)
		}
	}
	if fields["ID"] == "" {
		return "", errors.New("os-release has no ID")
	}
	return strings.TrimSpace(fields["ID"] + " " + fields["VERSION_ID"]), nil
}
