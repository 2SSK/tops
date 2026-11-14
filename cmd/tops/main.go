// Command tops manages a Temporal cluster over SSH
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	xterm "golang.org/x/term"

	"github.com/2SSK/tops/internal/cli"
	"github.com/2SSK/tops/internal/helper"
	"github.com/2SSK/tops/internal/term"
)

var opts = cli.Options{Getenv: os.Getenv, Getwd: os.Getwd, Geteuid: os.Geteuid, Node: helper.System, Passphrase: passphrase()}

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)

	err := cli.NewRootCommand(&opts).ExecuteContext(ctx)

	stop()

	if err != nil {
		reportError(opts, err)
	}

	os.Exit(term.ExitCode(err))
}

// passphrase asks on the terminal for an encrypted ssh.key's passphrase, without echo. It is
// nil when stdin is not a terminal (CI, a pipe): tops then refuses instead of waiting forever.
func passphrase() func(path string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !xterm.IsTerminal(fd) {
		return nil
	}
	return func(path string) ([]byte, error) {
		_, _ = fmt.Fprintf(os.Stderr, "passphrase for %s: ", path)
		defer func() { _, _ = fmt.Fprintln(os.Stderr) }()
		return xterm.ReadPassword(fd)
	}
}

// reportError prints err as one JSON document on stdout with --json,
// otherwise as what, why and fix on stderr.
// A Reported error was already shown.
func reportError(opts cli.Options, err error) {
	var reported *term.Reported
	if errors.As(err, &reported) {
		return
	}
	if opts.JSON {
		_ = term.WriteErrorJSON(os.Stdout, cli.Version, err)
		return
	}

	_ = term.PrintError(os.Stderr, cli.StyleFor(os.Stderr, opts), err)
}
