// Package version exposes build metadata injected at link time so every service
// can report exactly which build it is running.
package version

import (
	"fmt"
	"runtime"
)

// Build metadata. Overridden at build time via, for example:
//
//	go build -ldflags "-X github.com/netqo/pulse/internal/version.Version=v0.1.0"
var (
	// Version is the released semantic version, or "dev" for local builds.
	Version = "dev"
	// Commit is the short Git SHA the binary was built from.
	Commit = "none"
	// Date is the build timestamp in RFC 3339 form.
	Date = "unknown"
)

// String returns a single-line, human-readable build identifier.
func String() string {
	return fmt.Sprintf("pulse %s (commit %s, built %s, %s)",
		Version, Commit, Date, runtime.Version())
}
