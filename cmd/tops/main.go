// Command tops manages a Temporal cluster over SSH
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	xterm "golang.org/x/term"

	"github.com/2SSK/tops/internal/cli"
	"github.com/2SSK/tops/internal/term"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)

	err := cli.NewRootCommand().ExecuteContext(ctx)

	stop()

	if err != nil {
		style := term.Style{
			Unicode: term.UnicodeLocale(os.Getenv),
			Color:   term.ColorEnabled(os.Getenv, xterm.IsTerminal(int(os.Stderr.Fd()))),
		}

		_ = term.PrintError(os.Stderr, style, err)
	}

	os.Exit(term.ExitCode(err))
}
