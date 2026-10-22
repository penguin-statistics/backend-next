package bininfo

import "runtime/debug"

// RepositoryURL is the repository of the source code, as Datadog's source code integration takes it
// (git.repository_url) to link a span or a profile to the code at Commit.
const RepositoryURL = "https://github.com/penguin-statistics/backend-next"

var commit = func() string {
	info, _ := debug.ReadBuildInfo()
	return commitOf(GitCommit, info)
}()

// Commit returns the full SHA of the commit the binary was built from: GitCommit if it was injected, else the
// revision the go command stamps when it builds in a Git checkout (vcs.revision), else "".
func Commit() string {
	return commit
}

func commitOf(injected string, info *debug.BuildInfo) string {
	if injected != "" {
		return injected
	}
	if info != nil {
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				return s.Value
			}
		}
	}
	return ""
}
