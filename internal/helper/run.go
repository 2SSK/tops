package helper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/2SSK/tops/internal/wire"
)

// Exit codes of tops node <op>
const (
	ExitOK         = 0 // the response says ok
	ExitNotOK      = 1 // the response says why not
	ExitBadRequest = 2 // the request did not decode, or helper is not root
)

const (
	codeNotRoot    = "helper.not_root"
	codeBadRequest = "helper.bad_request"
	codeFailed     = "helper.failed"
)

// Env is what Run needs from the process. cmd/tops passes the real ones; tests fake them.
type Env struct {
	Host    Host
	Root    bool   // effective UID 0
	Version string // tops' own version, reported by hello
	Exe     string // this binary, whose sha256 hello reports
	Stdin   io.Reader
	Stdout  io.Writer
	Log     *slog.Logger
}

// handler runs one op on its raw args and returns its result.
type handler func(env Env, args json.RawMessage) (any, error)

var handlers = map[wire.Op]handler{
	wire.OpHello: hello,
	wire.OpFacts: facts,
}

// Run executes one op: it reads one request from stdin, writes one response to stdout,
// logs one record, and returns the process's exit code.
func Run(op wire.Op, env Env) int {
	if !env.Root {
		return env.reply(op, "", nil, &wire.Error{Code: codeNotRoot, Message: "tops node must run as root, through sudo"})
	}

	var req wire.Request
	if err := decodeStrict(env.Stdin, &req); err != nil {
		return env.reply(op, "", nil, badRequest("request", err))
	}
	handle, ok := handlers[op]
	if !ok {
		return env.reply(op, req.RunID, nil, badRequest("op", fmt.Errorf("unknown op %q", op)))
	}

	result, err := handle(env, req.Args)
	if err != nil {
		var we *wire.Error
		if !errors.As(err, &we) {
			we = &wire.Error{Code: codeFailed, Message: err.Error()}
		}
		return env.reply(op, req.RunID, nil, we)
	}
	return env.reply(op, req.RunID, result, nil)
}

func hello(env Env, args json.RawMessage) (any, error) {
	if err := decodeArgs(args, &struct{}{}); err != nil {
		return nil, err
	}
	h, err := env.Host.Hello(env.Version)
	if err != nil {
		return nil, err
	}
	if h.SHA256, err = fileSHA256(env.Exe); err != nil {
		return nil, err
	}
	return h, nil
}

func facts(env Env, args json.RawMessage) (any, error) {
	var a wire.FactsArgs
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	for path := range a.Want {
		if err := checkPath(path); err != nil {
			return nil, err
		}
	}
	for _, dir := range a.Dirs {
		if err := checkPath(dir); err != nil {
			return nil, err
		}
	}
	return env.Host.Facts(a)
}

// checkPath refuses a path that is not absolute and clean: the helper runs as root, so
// "../" must never reach the filesystem.
func checkPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return badRequest("args", fmt.Errorf("path %q is not absolute and clean", p))
	}
	return nil
}

// fileSHA256 returns the hex sha256 of a file's content.
func fileSHA256(name string) (string, error) {
	f, err := os.Open(filepath.Clean(name))
	if err != nil {
		return "", fmt.Errorf("hash own binary: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", fmt.Errorf("hash own binary: %w", err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// reply writes the response and the log record, and returns the exit code for them.
func (env Env) reply(op wire.Op, runID string, result any, opErr *wire.Error) int {
	resp := wire.Response{OK: opErr == nil, Error: opErr}
	if opErr == nil {
		b, err := json.Marshal(result)
		if err != nil {
			return env.reply(op, runID, nil, &wire.Error{Code: codeFailed, Message: "encode result: " + err.Error()})
		}
		resp.Result = b
	}
	if err := json.NewEncoder(env.Stdout).Encode(resp); err != nil {
		env.Log.Error("write response", "op", op, "run_id", runID, "err", err)
	}

	switch {
	case opErr == nil:
		env.Log.Info("op done", "op", op, "run_id", runID, "result", "ok")
		return ExitOK
	case opErr.Code == codeNotRoot || opErr.Code == codeBadRequest:
		env.Log.Error("op refused", "op", op, "run_id", runID, "code", opErr.Code, "message", opErr.Message)
		return ExitBadRequest
	default:
		env.Log.Error("op failed", "op", op, "run_id", runID, "code", opErr.Code, "message", opErr.Message)
		return ExitNotOK
	}
}

func badRequest(what string, err error) *wire.Error {
	return &wire.Error{Code: codeBadRequest, Message: what + ": " + err.Error()}
}

// decodeStrict decodes one JSON value and refuses fields the type does not have, so a
// typo in a request is an error rather than a silent default.
func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// decodeArgs decodes an op's args strictly. Missing arsg are the same as {}.
func decodeArgs(args json.RawMessage, v any) error {
	if len(args) == 0 || bytes.Equal(args, []byte("null")) {
		return nil
	}
	if err := decodeStrict(bytes.NewReader(args), v); err != nil {
		return badRequest("args", err)
	}
	return nil
}
