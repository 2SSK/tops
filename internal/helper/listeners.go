package helper

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/2SSK/tops/internal/wire"
)

// tcpListen is the state of a listening socket in /proc/net/tcp: TCP_LISTEN, in hex.
const tcpListen = "0A"

// Listeners returns every listening TCP socket, IPv4 and IPv6, with the processes that
// hold it, sorted by port and address.
func (h Host) Listeners() ([]wire.Listener, error) {
	owners, err := h.socketOwners()
	if err != nil {
		return nil, err
	}
	listeners := []wire.Listener{}
	for _, table := range []string{"net/tcp", "net/tcp6"} {
		found, err := h.listening(table, owners)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, found...)
	}
	slices.SortFunc(listeners, func(a, b wire.Listener) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Address, b.Address)
	})
	return listeners, nil
}

// listening reads one table of /proc/net and returns its listening sockets. A line is
// "sl local_address rem_address st ... uid timeout inode ...", addresses in hex.
func (h Host) listening(table string, owners map[string][]string) ([]wire.Listener, error) {
	b, err := os.ReadFile(filepath.Clean(filepath.Join(h.Proc, table)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	var found []wire.Listener
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 10 || f[3] != tcpListen {
			continue // the header, or not listening
		}
		addr, port, err := parseAddress(f[1])
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", table, i, err)
		}
		found = append(found, wire.Listener{Port: port, Address: addr, Process: strings.Join(owners[f[9]], ",")})
	}
	return found, nil
}

// parseAddress decodes "0100007F:1F90" (127.0.0.1:8080). The kernel prints the address
// as 32-bit words in host byte order, little-endian on amd64 and arm64, and the port in
// network order.
func parseAddress(s string) (string, int, error) {
	hexAddr, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, fmt.Errorf("bad address %q", s)
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return "", 0, fmt.Errorf("bad port in %q: %w", s, err)
	}
	raw, err := hex.DecodeString(hexAddr)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return "", 0, fmt.Errorf("bad address %q", s)
	}
	for w := 0; w < len(raw); w += 4 {
		slices.Reverse(raw[w : w+4])
	}
	addr, _ := netip.AddrFromSlice(raw)
	return addr.String(), int(port), nil
}

// socketOwners maps ecah socket inode to the sorted names of the processes holding it,
// from the /proc/<pid>/fd links that read "socket:[<inode>]". Processes that exit while
// this runs are skipped.
func (h Host) socketOwners() (map[string][]string, error) {
	pids, err := os.ReadDir(h.Proc)
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	owners := map[string][]string{}
	for _, p := range pids {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue // not a process
		}
		dir := filepath.Join(h.Proc, p.Name())
		comm, err := os.ReadFile(filepath.Clean(filepath.Join(dir, "comm")))
		if err != nil {
			continue
		}
		fds, _ := os.ReadDir(filepath.Join(dir, "fd"))
		for _, fd := range fds {
			target, _ := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
			inode, ok := strings.CutPrefix(target, "socket:[")
			if !ok {
				continue
			}
			inode = strings.TrimSuffix(inode, "]")
			name := strings.TrimSpace(string(comm))
			if !slices.Contains(owners[inode], name) {
				owners[inode] = append(owners[inode], name)
				slices.Sort(owners[inode])
			}
		}
	}
	return owners, nil
}
