package remote

import (
	"errors"
	"time"
)

// A node that misses keepaliveMisses replies in a row, keepaliveInterval apart, is lost
// (TRD "Keepalive").
const (
	keepaliveInterval = 15 * time.Second
	keepaliveMisses   = 3
)

var errLost = errors.New("the node stopped answering keepalives")

// keepalive asks the node for a reply every interval. When the node is lost it closes the
// connection, so every session on it fails now instead of hanging until TCP gives up.
func (c *Client) keepalive(interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for missed := 0; missed < keepaliveMisses; {
		select {
		case <-c.closed:
			return
		case <-tick.C:
		}
		if c.answers(interval) {
			missed = 0
		} else {
			missed++
		}
	}
	c.lost.Store(true)
	_ = c.ssh.Close()
}

// answers sends one keepalive and waits up to wait for any reply. OpenSSH answers
// keepalive@openssh.com with a failure, which still proves it is there.
func (c *Client) answers(wait time.Duration) bool {
	reply := make(chan error, 1)
	go func() {
		_, _, err := c.ssh.SendRequest("keepalive@openssh.com", true, nil)
		reply <- err
	}()
	select {
	case err := <-reply:
		return err == nil
	case <-time.After(wait):
		return false
	}
}
