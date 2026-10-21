// Package buildinfo holds the version stamped into release builds with
// -ldflags "-X github.com/endview/freeagent/internal/buildinfo.Version=<v>
// -X github.com/endview/freeagent/internal/buildinfo.Commit=<sha>".
// Source builds keep the defaults.
package buildinfo

const (
	DefaultVersion = "dev"
	DefaultCommit  = "unknown"
)

var (
	Version = DefaultVersion
	Commit  = DefaultCommit
)
