// Command hermes is the Hermes Cloud Foundation CLI.
package main

import (
	"os"

	"github.com/nityam02/hermes-cloud-foundation/internal/cli"
)

// Build metadata, injected with -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Execute(cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
