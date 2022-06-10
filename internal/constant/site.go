package constant

import "time"

const (
	SiteDefaultHost = "penguin-stats.io"

	SiteGlobalMirrorHost        = "penguin-stats.io"
	SiteChinaMainlandMirrorHost = "penguin-stats.cn"

	ShimCompatibilityHeaderKey   = "X-Penguin-Compatible"
	ShimCompatibilityHeaderValue = "frontend-v2@v3.4.0"

	DefaultServer = "CN"
)

var SiteHosts = []string{
	SiteGlobalMirrorHost,
	SiteChinaMainlandMirrorHost,
}

var Servers = []string{
	"CN",
	"TW",
	"US",
	"JP",
	"KR",
}

var ServerMap = map[string]struct{}{
	"CN": {},
	"TW": {},
	"US": {},
	"JP": {},
	"KR": {},
}

var LocMap = map[string]*time.Location{
	"CN": time.FixedZone("UTC+8", +8*60*60),
	"TW": time.FixedZone("UTC+8", +8*60*60),
	"US": time.FixedZone("UTC-7", -7*60*60),
	"JP": time.FixedZone("UTC+9", +9*60*60),
	"KR": time.FixedZone("UTC+9", +9*60*60),
}
