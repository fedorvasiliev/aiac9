// Package version holds build-time metadata, injected via -ldflags at build
// time (see Makefile).
package version

import "fmt"

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

// String returns a human-readable version string.
func String() string {
	return fmt.Sprintf("aiac9 %s (commit %s, built %s)", Version, Commit, BuildDate)
}
