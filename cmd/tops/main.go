// Command tops manages a Temporal cluster over SSH
package main

import (
	"os"

	"github.com/2SSK/tops/internal/cli"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
