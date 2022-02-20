package constants

const (
	SiteDefaultHost = "penguin-stats.io"

	SiteGlobalMirrorHost        = "penguin-stats.io"
	SiteChinaMainlandMirrorHost = "penguin-stats.cn"

	ShimCompatibilityHeaderKey   = "X-Penguin-Compatible"
	ShimCompatibilityHeaderValue = "frontend-v2@v3.4.0"
)

var SiteHosts = []string{
	SiteGlobalMirrorHost,
	SiteChinaMainlandMirrorHost,
}
