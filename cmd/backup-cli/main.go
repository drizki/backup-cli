// Command backup-cli creates full, independent, restorable-with-cp backups.
package main

import (
	"os"
	"runtime/debug"

	"github.com/drizki/backup-cli/internal/cli"
	"github.com/drizki/backup-cli/internal/engine"
	"github.com/drizki/backup-cli/internal/memlimit"
)

func main() {
	os.Exit(run())
}

func run() int {
	memlimit.Apply(os.Getenv, memlimit.CgroupFile, debug.SetMemoryLimit)
	cli.RegisterEngine(engine.Run)
	return cli.Execute()
}
