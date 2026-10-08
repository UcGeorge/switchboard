// Command switchboard runs an OpenAI-compatible API whose requests are
// answered by agents connected over MCP.
package main

import (
	"os"

	"github.com/ucgeorge/switchboard/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
