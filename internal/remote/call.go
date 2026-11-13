package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/2SSK/tops/internal/term"
	"github.com/2SSK/tops/internal/wire"
	"golang.org/x/crypto/ssh"
)

// helperDir is where one helper build lives on a node, named bythe first 16 hex digits of
// its sha256: two builds never share a path, so there is no version to compare (TRD "Helper path").
func helperDir(sha256 string) string { return "/usr/local/lib/tops/" + sha256[:16] }

// cmdHelper runs one op as root. The op name is on the command line, so sudo logs it; its
// arguments go on stdin, never in argv. sha256 is hex we computed and op is a wire constant,
// so nothing from the spec reaches the command.
func cmdHelper(sha256 string, op wire.Op) string {
	return "sudo -n " + helperDir(sha256) + "/tops node " + string(op)
}

// errNoHelper: the node has no helper at that path yet; upload it.
var errNoHelper = errors.New("the helper is not installed")

func (c *Client) call(ctx context.Context, sha256 string, op wire.Op, req wire.Request, result any) error {
	in, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode the %s request: %w", op, err)
	}
	var stdout, stderr bytes.Buffer
	err = c.run(ctx, cmdHelper(sha256, op), bytes.NewReader(in), &stdout, &stderr)
	var exit *ssh.ExitError
	if err != nil && !errors.As(err, &exit) {
		return err // the connection or the context, not the helper
	}
	var resp wire.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return c.noResponse(op, stdout.Len(), stderr.String(), exit)
	}
	if !resp.OK {
		return c.helperError(op, resp.Error)
	}
	if err := json.Unmarshal(resp.Result, result); err != nil {
		return fmt.Errorf("decode the %s result from %s: %w", op, c.Node, err)
	}
	return nil
}

// noResponse explains a call whose stdout was not a response: no helper at that path (sudo
// says "command not found"), or a helper that crashed before it could answer.
func (c *Client) noResponse(op wire.Op, stdoutLen int, stderr string, exit *ssh.ExitError) error {
	if stdoutLen == 0 && strings.Contains(stderr, ": command not found") {
		return errNoHelper
	}
	status := 0
	if exit != nil {
		status = exit.ExitStatus()
	}
	return fmt.Errorf("%s on %s: no response from the helper (exit %d): %s", op, c.Node, status, strings.TrimSpace(stderr))
}

func (c *Client) helperError(op wire.Op, e *wire.Error) error {
	if e == nil {
		e = &wire.Error{Code: "helper.failed", Message: "not ok, and no error"}
	}
	return &term.UserError{Code: e.Code, What: fmt.Sprintf("%s on %s: %s", op, c.Node, e.Message), Why: e.Detail}
}
