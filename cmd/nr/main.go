package main

import (
	"fmt"
	"os"

	"github.com/noderings/cli/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, cli.FormatUserError(err))
		os.Exit(cli.ExitCode(err))
	}
}
