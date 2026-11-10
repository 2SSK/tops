package helper

import (
	"runtime"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// node is a fake 16 GiB, 12-CPU VPS with no cgroup limit, as the files hello reads.
func node() map[string]string {
	return map[string]string{
		"proc/meminfo":             meminfo,
		"proc/stat":                stat,
		"proc/1/cgroup":            "0::/init.scope\n",
		"proc/sys/kernel/hostname": "vps-01\n",
		"etc/machine-id":           "99dd89667394438f8f128b862b8bb427\n",
		"etc/os-release":           "PRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\nID_LIKE=debian\n",
	}
}

// change returns node() with name set to content, or removed when content is "".
func change(name, content string) map[string]string {
	files := node()
	if content == "" {
		delete(files, name)
	} else {
		files[name] = content
	}
	return files
}

func TestHello(t *testing.T) {
	got, err := withAffinity(fakeHost(t, node()), 12).Hello("v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := wire.Hello{
		HelperVersion: "v0.2.0",
		Hostname:      "vps-01",
		MachineID:     "99dd89667394438f8f128b862b8bb427",
		OS:            "ubuntu 24.04",
		Arch:          runtime.GOARCH,
		Memory:        wire.Detected{Value: 16 * gib, Source: "meminfo", Machine: 16 * gib},
		Cores:         wire.Detected{Value: 12, Source: "affinity", Machine: 12},
	}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestHelloOS(t *testing.T) {
	tests := []struct {
		name, osRelease, want string
	}{
		{"Ubuntu", "ID=ubuntu\nVERSION_ID=\"24.04\"\n", "ubuntu 24.04"},
		{"Debian, single quotes", "ID='debian'\nVERSION_ID='12'\n", "debian 12"},
		{"rolling release: no VERSION_ID", "NAME=\"EndeavourOS\"\nID=\"endeavouros\"\nID_LIKE=\"arch\"\n", "endeavouros"},
		{"comments and blank lines", "# a comment\n\nID=ubuntu\nVERSION_ID=24.04\n", "ubuntu 24.04"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := withAffinity(fakeHost(t, change("etc/os-release", tt.osRelease)), 12).Hello("v")
			if err != nil {
				t.Fatal(err)
			}
			if got.OS != tt.want {
				t.Errorf("OS %q, want %q", got.OS, tt.want)
			}
		})
	}
}

func TestHelloErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"no hostname", change("proc/sys/kernel/hostname", "")},
		{"empty hostname", change("proc/sys/kernel/hostname", "\n")},
		{"no machine-id", change("etc/machine-id", "")},
		{"empty machine-id", change("etc/machine-id", " \n")},
		{"no os-release", change("etc/os-release", "")},
		{"os-release without ID", change("etc/os-release", "NAME=Ubuntu\nVERSION_ID=24.04\n")},
		{"no meminfo", change("proc/meminfo", "")},
		{"no stat", change("proc/stat", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := withAffinity(fakeHost(t, tt.files), 12).Hello("v"); err == nil {
				t.Errorf("got %+v, want an error", got)
			}
		})
	}
}
