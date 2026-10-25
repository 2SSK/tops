// Command tops manages a Temporal cluster over SSH
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	xterm "golang.org/x/term"

	"github.com/2SSK/tops/internal/cli"
	"github.com/2SSK/tops/internal/term"
)

var opts = cli.Options{Getenv: os.Getenv, Getwd: os.Getwd}

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

	style := term.Style{
		Unicode: term.UnicodeLocale(os.Getenv),
		Color:   !opts.NoColor && term.ColorEnabled(os.Getenv, xterm.IsTerminal(int(os.Stderr.Fd()))),
	}

	_ = term.PrintError(os.Stderr, style, err)
}
