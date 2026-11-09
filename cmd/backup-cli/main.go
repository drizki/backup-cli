// Command backup-cli creates full, independent, restorable-with-cp backups.
package main

import (
	"os"

	"github.com/drizki/backup-cli/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	return cli.Execute()
}
