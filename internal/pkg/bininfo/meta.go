// This file's content is intended to be used with go's -ldflags option to inject version control information.
// DO NOT EDIT THE VARIABLE NAMES UNLESS YOU KNOW WHAT YOU ARE DOING.

package bininfo

var (
	// GitTag is the git tag used to identify the current build.
	GitTag = "v0.0.0"

	// GitCommit is the commit hash used to build this binary.
	GitCommit = "unknown"

	// BuildTime is the time at which the application was built.
	BuildTime = "1970-01-01T00:00:00Z"
)
