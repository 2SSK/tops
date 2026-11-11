package helper

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// The headers of /proc/net/tcp and tcp6: a table with no sockets.
const (
	tcpHeader  = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	tcp6Header = "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
)

// vps-01's tables, copied from the lab: sshd on 22 (IPv4 and IPv6, held by sshd and by
// systemd's socket activation), Docker's DNS on 127.0.0.11 with no process visible, and
// one established connection, which is not a listener.
const (
	vps01TCP = tcpHeader +
		"   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 24923 1 00000000742a82c6 100 0 0 10 0\n" +
		"   1: 0B00007F:932B 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 20842 1 00000000b9902eff 100 0 0 10 0\n" +
		"   2: 0B00500A:0016 0100500A:AC8A 01 00000064:00000000 01:00000014 00000000     0        0 463811 3 000000000112ff6d 20 4 19 10 -1\n"
	vps01TCP6 = tcp6Header +
		"   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 21025 1 00000000b6ab0996 100 0 0 10 0\n"
)

// procWithSockets is node() with vps-01's tables, and processes holding the sockets.
func procWithSockets(t *testing.T) Host {
	t.Helper()
	files := node()
	files["proc/net/tcp"] = vps01TCP
	files["proc/net/tcp6"] = vps01TCP6
	files["proc/1/comm"] = "systemd\n"
	files["proc/2171/comm"] = "sshd\n"
	files["proc/self/comm"] = "not a pid\n"
	files["proc/999/fd/.keep"] = "" // a process that exited: no comm left
	h := withAffinity(fakeHost(t, files), 12)

	for link, target := range map[string]string{
		"1/fd/44":   "socket:[24923]",
		"1/fd/45":   "socket:[21025]",
		"1/fd/0":    "/dev/null",
		"2171/fd/3": "socket:[24923]",
		"2171/fd/4": "socket:[21025]",
		"2171/fd/5": "socket:[24923]", // the same socket twice in one process
	} {
		p := filepath.Join(h.Proc, link)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestListeners(t *testing.T) {
	got, err := procWithSockets(t).Listeners()
	if err != nil {
		t.Fatal(err)
	}
	want := []wire.Listener{
		{Port: 22, Address: "0.0.0.0", Process: "sshd,systemd"},
		{Port: 22, Address: "::", Process: "sshd,systemd"},
		{Port: 37675, Address: "127.0.0.11", Process: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestListenersNone(t *testing.T) {
	got, err := withAffinity(fakeHost(t, node()), 12).Listeners()
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("got %#v, %v; want an empty list, which JSON writes as []", got, err)
	}
}

func TestParseAddress(t *testing.T) {
	tests := []struct {
		in   string
		addr string
		port int
	}{
		{"0100007F:1F90", "127.0.0.1", 8080},
		{"00000000:1C41", "0.0.0.0", 7233},
		{"0D00500A:1C41", "10.80.0.13", 7233},
		{"00000000000000000000000001000000:1C41", "::1", 7233},
		{"0000000000000000FFFF00000B00500A:1C41", "::ffff:10.80.0.11", 7233},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			addr, port, err := parseAddress(tt.in)
			if err != nil || addr != tt.addr || port != tt.port {
				t.Errorf("got %s %d %v, want %s %d", addr, port, err, tt.addr, tt.port)
			}
		})
	}
}

func TestListenersErrors(t *testing.T) {
	bad := func(line string) map[string]string {
		return change("proc/net/tcp", tcpHeader+"   0: "+line+" 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0 100 0 0 10 0\n")
	}
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"no tcp table", change("proc/net/tcp", "")},
		{"address without port", bad("0100007F")},
		{"port not hex", bad("0100007F:ZZZZ")},
		{"address not hex", bad("ZZ00007F:1F90")},
		{"address of 3 bytes", bad("00007F:1F90")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := withAffinity(fakeHost(t, tt.files), 12).Listeners(); err == nil {
				t.Errorf("got %+v, want an error", got)
			}
		})
	}

	t.Run("no /proc", func(t *testing.T) {
		h := Host{Proc: filepath.Join(t.TempDir(), "gone")}
		if got, err := h.Listeners(); err == nil {
			t.Errorf("got %+v, want an error", got)
		}
	})
}
