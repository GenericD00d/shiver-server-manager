// Command shiver-server-manager sets up and runs Sharkord servers.
package main

import (
	"os"

	"github.com/GenericD00d/shiver-server-manager/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Main(version, os.Args[1:]))
}
