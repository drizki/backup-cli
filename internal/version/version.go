// Package version holds build-time metadata injected via -ldflags.
package version

import "fmt"

// Set by the linker: -X github.com/drizki/backup-cli/internal/version.Version=...
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a one-line human readable version.
func String() string {
	return fmt.Sprintf("%s (commit %s, built %s)", Version, Commit, Date)
}
