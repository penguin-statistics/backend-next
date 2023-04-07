package test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/mod/semver"
)

func TestRejectRuleSemVer(t *testing.T) {
	assert.True(t, semver.IsValid("v4.13.0-beta.4.3.gfe735461"))
	assert.True(t, semver.Compare("v4.13.0-beta.4", "v4.13.0-rc.1") < 0, "v4.13.0-beta.4 < v4.13.0-rc.1")
	assert.True(t, semver.Compare("v4.13.0-beta.4.3", "v4.13.0-rc.1") < 0, "v4.13.0-beta.4.3 < v4.13.0-rc.1")
	assert.True(t, semver.Compare("v4.13.0-beta.4.3.gfe735461", "v4.13.0-rc.1") < 0, "v4.13.0-beta.4.3.gfe735461 < v4.13.0-rc.1")
	assert.True(t, semver.Compare("v4.13.0-rc.1", "v4.13.0-rc.1") == 0, "v4.13.0-rc.1 == v4.13.0-rc.1")
	assert.True(t, semver.Compare("v4.13.0-rc.1", "v4.13.0-rc.2") < 0, "v4.13.0-rc.1 < v4.13.0-rc.2")
	assert.True(t, semver.Compare("v4.13.0-rc.1", "v4.13.0") < 0, "v4.13.0-rc.1 < v4.13.0")
}
