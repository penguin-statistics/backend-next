// This file's content is intended to be used with go's -ldflags option to inject version control information.
// DO NOT EDIT THE VARIABLE NAMES UNLESS YOU KNOW WHAT YOU ARE DOING.

package bininfo

var (
	// Version is the SemVer version of the binary.
	// Git commit is appended, if available, separated by a plus sign [+].
	Version = "v0.0.0"

	// BuildTime is the time at which the application was built.
	BuildTime = "1970-01-01T00:00:00Z"
)
