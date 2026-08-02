package main

import (
	"context"
	"os"
	"runtime/debug"

	"github.com/bobashopcashier/kalshi-cli/internal/cli"
)

var version = "dev"

func main() {
	app := cli.New(cli.Config{
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Version: buildVersion(),
		IsTTY:   isTerminal(os.Stdout),
	})
	os.Exit(app.Run(context.Background(), os.Args[1:]))
}

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	return resolveVersion(version, info, ok)
}

func resolveVersion(injected string, info *debug.BuildInfo, ok bool) string {
	if injected != "" && injected != "dev" {
		return injected
	}
	if ok && info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	if injected != "" {
		return injected
	}
	return "dev"
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
