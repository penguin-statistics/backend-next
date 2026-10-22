package bininfo

import (
	"runtime/debug"
	"testing"
)

func TestCommitOf(t *testing.T) {
	stamped := &debug.BuildInfo{Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "5b639dd0000000000000000000000000000000aa"},
	}}
	for _, tt := range []struct {
		name     string
		injected string
		info     *debug.BuildInfo
		want     string
	}{
		{"injected", "7db45fb038487dc8f3d5c8472148a63cdd90bb3a", stamped, "7db45fb038487dc8f3d5c8472148a63cdd90bb3a"},
		{"stamped by the go command", "", stamped, "5b639dd0000000000000000000000000000000aa"},
		{"unknown", "", &debug.BuildInfo{}, ""},
		{"no build info", "", nil, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := commitOf(tt.injected, tt.info); got != tt.want {
				t.Errorf("commitOf = %q, want %q", got, tt.want)
			}
		})
	}
}
