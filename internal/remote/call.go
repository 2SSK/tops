package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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

// replyMargin is how much longer the workstation waits for a helper op than the helper lets
// it run: a helper that runs out of time still answers why.
const replyMargin = 15 * time.Second

func timeout(op wire.Op) time.Duration { return wire.Timeout(op) + replyMargin }

// errNoHelper: the node has no helper at that path yet; upload it.
var errNoHelper = errors.New("the helper is not installed")

func (c *Client) call(ctx context.Context, sha256 string, op wire.Op, req wire.Request, result any) error {
	in, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("encode the %s request: %w", op, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(op))
	defer cancel()
	var stdout, stderr bytes.Buffer
	err = c.run(ctx, cmdHelper(sha256, op), bytes.NewReader(in), &stdout, &stderr)
	c.logHelper(op, stderr.Bytes())
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

// logHelper copies the helper's own log, slog JSON on stderr, into tops' log: one record per
// line, with the op and the node.
func (c *Client) logHelper(op wire.Op, stderr []byte) {
	for line := range bytes.Lines(stderr) {
		c.log.Debug("helper", "op", op, "record", string(bytes.TrimSpace(line)))
	}
}

// nodeOp runs one op on the node's helper with the run's request and args, and decodes its
// result.
func nodeOp[T any](ctx context.Context, n *Node, op wire.Op, args any) (T, error) {
	var result T
	req := n.req
	var err error
	if req.Args, err = json.Marshal(args); err != nil {
		return result, fmt.Errorf("encode the %s args: %w", op, err)
	}
	if err := n.call(ctx, n.SHA256, op, req, &result); err != nil {
		var zero T
		return zero, err
	}
	return result, nil
}

// Facts asks the node's helper what is there now, compared with what  tops would write.
func (n *Node) Facts(ctx context.Context, args wire.FactsArgs) (wire.Facts, error) {
	return nodeOp[wire.Facts](ctx, n, wire.OpFacts, args)
}

// Stage installs the temporal user and releases on the node. The node downloads each archive
// and checks it against the sha256 the workstation read from checksums.txt.
func (n *Node) Stage(ctx context.Context, s wire.Stage) (wire.StageResult, error) {
	return nodeOp[wire.StageResult](ctx, n, wire.OpStage, s)
}

// Validate checks each role's files with the release staged on the node. The helper runs
// its temporal-server in /run/tops/<run-id> and removes the directory afterwards.
func (n *Node) Validate(ctx context.Context, v wire.Validate) (wire.ValidateResult, error) {
	return nodeOp[wire.ValidateResult](ctx, n, wire.OpValidate, v)
}

// Schema creates the databases the spec names where missing and migrates them with the
// staged release's temporal-sql-tool, run on the node: PostgreSQL admits the nodes, not the
// workstation. The password travels in the request, on the helper's stdin.
func (n *Node) Schema(ctx context.Context, s wire.Schema) error {
	_, err := nodeOp[struct{}](ctx, n, wire.OpSchema, s)
	return err
}

// Lock acquires, renews or releases the node's lease. The helper names the lease after the
// request's run, so every later fenced op of the run is checked against it.
func (n *Node) Lock(ctx context.Context, l wire.Lock) (wire.LockResult, error) {
	return nodeOp[wire.LockResult](ctx, n, wire.OpLock, l)
}

// Write puts files in place on the node and removes others, after the helper saved what was
// there in the apply's snapshot. The contents, secrets among them, travel on stdin.
func (n *Node) Write(ctx context.Context, w wire.Write) (wire.WriteResult, error) {
	return nodeOp[wire.WriteResult](ctx, n, wire.OpWrite, w)
}

// Restore brings back every path an apply changed on the node, from the snapshot the helper
// saved, after it saved what is there now under the run's own apply id.
func (n *Node) Restore(ctx context.Context, r wire.Restore) (wire.WriteResult, error) {
	return nodeOp[wire.WriteResult](ctx, n, wire.OpRestore, r)
}

// Service runs one systemd job on a role's unit and returns the unit's state after it.
func (n *Node) Service(ctx context.Context, s wire.Service) (wire.UnitState, error) {
	return nodeOp[wire.UnitState](ctx, n, wire.OpService, s)
}

// AppendHistory keeps the run's record on the node, named by the run's apply id; the helper
// sets the record's run and apply id from the request. A retry replaces the record.
func (n *Node) AppendHistory(ctx context.Context, r wire.History) error {
	_, err := nodeOp[struct{}](ctx, n, wire.OpHistory, wire.HistoryArgs{Action: wire.HistoryAppend, Record: &r})
	return err
}

// ListHistory returns the node's newest records, up to limit (0 is 20), the newest first.
func (n *Node) ListHistory(ctx context.Context, limit int) ([]wire.History, error) {
	res, err := nodeOp[wire.HistoryResult](ctx, n, wire.OpHistory, wire.HistoryArgs{Action: wire.HistoryList, Limit: limit})
	return res.Records, err
}
