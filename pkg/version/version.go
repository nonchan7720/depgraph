// Package version holds the build's version information.
//
// Version is bumped by release-please on every release (the
// x-release-please-version marker below), so a source build of a release
// tag reports that version. Release builds made by goreleaser override all
// three variables with -ldflags -X.
package version

var (
	// Version is the semantic version, without a leading "v".
	Version = "0.0.0" // x-release-please-version
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the build time (RFC 3339).
	Date = "unknown"
)
