package main

import (
	"fmt"
	"os"

	"codex-switch/internal/cli"
)

func main() {
	cmd, err := cli.NewRootCmd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
