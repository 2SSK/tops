// Command tops manages a Temporal cluster over SSH
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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
		_ = term.PrintError(os.Stderr, err)
	}

	os.Exit(term.ExitCode(err))
}
