// Command backup-cli creates full, independent, restorable-with-cp backups.
package main

import (
	"os"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/engine"
)

func main() {
	os.Exit(run())
}

func run() int {
	cli.RegisterEngine(engine.Run)
	return cli.Execute()
}
