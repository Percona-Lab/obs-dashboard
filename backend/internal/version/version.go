// Package version holds the build version. Release images stamp it with
// -ldflags "-X github.com/percona/obs-dashboard/internal/version.Version=vX.Y.Z".
package version

// Version is "dev" for local builds.
var Version = "dev"
