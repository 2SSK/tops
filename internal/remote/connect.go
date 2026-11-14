package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
)

// cmdUpload installs the helper from stdin, atomically: a half-written binary is never at
// the path cmdHelper runs. The directory is hex we computed, so nothing is interploated.
func cmdUpload(sha256 string) string {
	return `sudo -n sh -c 'set -e; d=` + helperDir(sha256) + `; mkdir -p -m 0755 "$d"; cat > "$d/tops.$$"; chmod 0755 "$d/tops.$$"; mv "$d/tops.$$" "$d/tops"'`
}

// binary is a linux helper build on the workstation.
type binary struct {
	path   string
	sha256 string // hex
}

// binaryFor finds the linux helper for a node's arch: this executable when it is one, else
// tops-linux-<arch> beside it, which make build-linux writes.
func binaryFor(exe, goos, goarch, arch string) (string, error) {
	if goos == "linux" && goarch == arch {
		return exe, nil
	}
	beside := filepath.Join(filepath.Dir(exe), "tops-linux-"+arch)
	if _, err := os.Stat(beside); err != nil {
		return "", &term.UserError{
			Code: "helper.no_binary",
			What: "no linux/" + arch + " helper for this tops",
			Why:  fmt.Sprintf("this tops is %s/%s and %s does not exist", goos, goarch, beside),
			Fix:  "make build-linux",
		}
	}
	return beside, nil
}

// binary returns the helper for arch with its sha256, hashed once per run.
func (d *Dialer) binary(arch string) (binary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if b, ok := d.bins[arch]; ok {
		return b, nil
	}
	path, err := binaryFor(d.exe, d.goos, d.goarch, arch)
	if err != nil {
		return binary{}, err
	}
	f, err := os.Open(path) //nolint:gosec // our own executable or the build beside it
	if err != nil {
		return binary{}, fmt.Errorf("read the helper: %w", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return binary{}, fmt.Errorf("hash %s: %w", path, err)
	}
	b := binary{path: path, sha256: hex.EncodeToString(h.Sum(nil))}
	d.bins[arch] = b
	return b, nil
}

// Node is a connected node whose helper answered hello: what every command works with.
type Node struct {
	*Client
	Arch     string     // GOARCH, from uname -m
	SHA256   string     // the helper's
	Hello    wire.Hello // what the helper detected
	Uploaded bool       // this run installed the helper
}

// Connect dials a ndoe, runs the preflight, and makes sure the helper of this exact build is
// there, uploading it when it is not. req carries the run's id; its args are ignored.
func (d *Dialer) Connect(ctx context.Context, name, address string, req wire.Request) (*Node, error) {
	c, err := d.Dial(ctx, name, address)
	if err != nil {
		return nil, err
	}
	n, err := d.connect(ctx, c, req)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return n, nil
}

func (d *Dialer) connect(ctx context.Context, c *Client, req wire.Request) (*Node, error) {
	arch, err := c.preflight(ctx)
	if err != nil {
		return nil, err
	}
	bin, err := d.binary(arch)
	if err != nil {
		return nil, err
	}
	n := &Node{Client: c, Arch: arch, SHA256: bin.sha256}
	req.Args = nil
	err = c.call(ctx, bin.sha256, wire.OpHello, req, &n.Hello)
	if errors.Is(err, errNoHelper) {
		if err := c.upload(ctx, bin); err != nil {
			return nil, err
		}
		n.Uploaded = true
		err = c.call(ctx, bin.sha256, wire.OpHello, req, &n.Hello)
	}
	if err != nil {
		return nil, err
	}
	if n.Hello.SHA256 != bin.sha256 {
		return nil, &term.UserError{
			Code: "helper.failed",
			What: "the helper on " + c.Node + " is not this build",
			Why:  fmt.Sprintf("%s/tops reports sha256 %s, want %s", helperDir(bin.sha256), n.Hello.SHA256, bin.sha256),
			Fix:  "remove " + helperDir(bin.sha256) + " on " + c.Node + "; the next run uploads it again",
		}
	}
	return n, nil
}

// upload sends the helper binary on stdin to the one f ixed upload command.
func (c *Client) upload(ctx context.Context, bin binary) error {
	f, err := os.Open(bin.path)
	if err != nil {
		return fmt.Errorf("read the helper: %w", err)
	}
	defer func() { _ = f.Close() }()
	var stderr bytes.Buffer
	if err := c.run(ctx, cmdUpload(bin.sha256), f, io.Discard, &stderr); err != nil {
		return fmt.Errorf("upload the helper to %s: %w: %s", c.Node, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}
