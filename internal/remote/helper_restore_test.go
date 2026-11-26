//go:build helper

package remote

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"testing"

	"github.com/2SSK/tops/internal/wire"
)

// tree is the mode, owner, group and sha256 of every file under /etc/temporal and of the
// history drop-in, as the node has them.
func (c *container) tree(t *testing.T) string {
	t.Helper()
	return docker(t, nil, "exec", c.id, "sh", "-c",
		"find /etc/temporal /etc/systemd/system/temporal@history.service.d -type f -exec stat -c '%a %U %G %n' {} + -exec sha256sum {} + | sort")
}

// TestHelperRestore: as root on a real node, restore brings an apply's files back byte for
// byte, with their modes and owners, an owner without an account among them.
func TestHelperRestore(t *testing.T) {
	c, d, n := writeNode(t)
	if _, err := n.Write(t.Context(), renderedFiles); err != nil {
		t.Fatal(err)
	}
	docker(t, nil, "exec", c.id, "sh", "-c", "echo 'hand edit' > /etc/temporal/history.yaml && chmod 604 /etc/temporal/history.yaml && chown 4321:4321 /etc/temporal/history.yaml")
	before := c.tree(t)
	files := renderedFiles.Files
	if _, err := c.apply(t, d, "20261124T130000Z-bbbb").Write(t.Context(), wire.Write{Files: files[1:2], Remove: []string{files[0].Path, files[2].Path}}); err != nil {
		t.Fatal(err)
	}
	if c.tree(t) == before {
		t.Fatal("the second apply changed nothing")
	}
	if r, err := c.apply(t, d, "20261124T140000Z-cccc").Restore(t.Context(), wire.Restore{Snapshot: "20261124T130000Z-bbbb"}); err != nil || len(r.Written) != 3 {
		t.Fatalf("restore = %+v, %v", r, err)
	}
	if after := c.tree(t); after != before {
		t.Errorf("restored:\n%s\nwant:\n%s", after, before)
	}
}

// killMidWrite kills the node's helper with SIGKILL as soon as its temporary file exists. It
// finds the helper's pid first and uses only shell builtins, so it is faster than the write.
const killMidWrite = `pid=
until [ "$pid" ]; do
	for p in /proc/[0-9]*; do
		read -r comm 2>/dev/null <$p/comm && [ "$comm" = tops ] && pid=${p#/proc/}
	done
done
until compgen -G '/etc/temporal/.*.tops-*' >/dev/null; do kill -0 $pid 2>/dev/null || exit 0; done
kill -9 $pid 2>/dev/null
exit 0`

// bigFile is a history.yaml of 16 MiB of b, and what sha256sum prints for it.
func bigFile(b byte) (wire.Write, string) {
	content := bytes.Repeat([]byte{b}, 16<<20)
	sum := sha256.Sum256(content)
	return wire.Write{Files: []wire.File{{Path: "/etc/temporal/history.yaml", Content: content, Mode: "0640", Owner: "root", Group: "temporal"}}},
		hex.EncodeToString(sum[:]) + "  /etc/temporal/history.yaml"
}

// killedWrite writes w while killMidWrite runs, and returns the file's sha256 line and
// whether the helper died before its rename: its temporary file is still there.
func (c *container) killedWrite(t *testing.T, n *Node, w wire.Write) (string, bool) {
	t.Helper()
	killed := make(chan struct{})
	go func() {
		defer close(killed)
		_ = exec.Command("docker", "exec", c.id, "timeout", "30", "bash", "-c", killMidWrite).Run() //nolint:noctx,gosec // the test's own container; timeout bounds it
	}()
	_, _ = n.Write(t.Context(), w) // killed: no response
	<-killed
	left := docker(t, nil, "exec", c.id, "ls", "-A", "/etc/temporal")
	return docker(t, nil, "exec", c.id, "sha256sum", "/etc/temporal/history.yaml"), left != "history.yaml"
}

// TestHelperKilledMidWrite: a helper killed with SIGKILL before its rename leaves the old
// file whole; killed after it, the new one: never a part. The next write succeeds: the
// kernel released the fence, and the leftover is swept.
func TestHelperKilledMidWrite(t *testing.T) {
	c, _, n := writeNode(t)
	first, oldSum := bigFile('0')
	if _, err := n.Write(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	for i := byte('1'); i <= '9'; i++ {
		next, newSum := bigFile(i)
		got, mid := c.killedWrite(t, n, next)
		if got != oldSum && (mid || got != newSum) {
			t.Fatalf("killed (before the rename: %v): %s, want the old or the new file", mid, got)
		}
		if _, err := n.Write(t.Context(), next); err != nil {
			t.Fatalf("the write after the kill: %v", err)
		}
		if left := docker(t, nil, "exec", c.id, "ls", "-A", "/etc/temporal"); left != "history.yaml" {
			t.Fatalf("left behind: %s", left)
		}
		if mid {
			return
		}
		oldSum = newSum
	}
	t.Error("no kill landed before the rename")
}

// TestHelperUmask: a sudo that hands the helper umask 077 does not keep the temporal user out
// of the directories tops creates.
func TestHelperUmask(t *testing.T) {
	c, _, n := writeNode(t)
	docker(t, nil, "exec", c.id, "sh", "-c", "echo 'Defaults umask=0077' > /etc/sudoers.d/umask && chmod 440 /etc/sudoers.d/umask")
	if _, err := n.Write(t.Context(), renderedFiles); err != nil {
		t.Fatal(err)
	}
	if got := docker(t, nil, "exec", c.id, "stat", "-c", "%a", "/etc/temporal", "/etc/systemd/system/temporal@history.service.d"); got != "755\n755" {
		t.Errorf("directories %q, want 755", got)
	}
}
