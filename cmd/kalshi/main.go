package main

import (
	"context"
	"os"

	"kalshi-cli/internal/cli"
)

var version = "dev"

func main() {
	app := cli.New(cli.Config{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: version,
		IsTTY:   isTerminal(os.Stdout),
	})
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
