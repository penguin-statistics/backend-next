// This file's content is intended to be used with go's -ldflags option to inject version control information.

package bininfo

var (
	// GitTag is the git tag used to identify the current build.
	GitTag = "v0.0.0"

	// GitCommit is the commit hash used to build this binary.
	GitCommit = "unknown"

	// GitBranch is the branch used to build this binary.
	GitBranch = "unknown"

	// BuildTime is the time at which the application was built.
	BuildTime = "1970-01-01T00:00:00Z"
)
