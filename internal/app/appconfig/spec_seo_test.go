package appconfig

import (
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// The SEO page settings are set by environment variables in the manifests: keep their names.
func TestSEOPageSettings(t *testing.T) {
	// the settings required without a default
	for _, key := range []string{"DROP_REPORT_ARCHIVE_S3_BUCKET", "DROP_REPORT_ARCHIVE_S3_REGION", "AWS_ACCESS_KEY", "AWS_SECRET_KEY", "POSTGRES_DSN"} {
		t.Setenv("PENGUIN_V3_"+key, "x")
	}

	var defaults ConfigSpec
	if err := envconfig.Process("penguin_v3", &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults.SEOPagesEnabled {
		t.Error("SEO pages are enabled by default")
	}
	if defaults.SEOShellURL != "http://frontend-rollout.default.svc.cluster.local/" || defaults.SEOSiteURL != "https://penguin-stats.io" ||
		defaults.SEOShellTTL != 15*time.Second || defaults.SEOShellFetchTimeout != 3*time.Second || defaults.SEOPageMaxAge != 5*time.Minute {
		t.Errorf("defaults: %+v", defaults)
	}

	t.Setenv("PENGUIN_V3_SEO_PAGES_ENABLED", "true")
	t.Setenv("PENGUIN_V3_SEO_SHELL_URL", "https://penguin-stats.io/")
	t.Setenv("PENGUIN_V3_SEO_SHELL_TTL", "2m")
	t.Setenv("PENGUIN_V3_SEO_SHELL_FETCH_TIMEOUT", "1s")
	t.Setenv("PENGUIN_V3_SEO_SITE_URL", "http://localhost:9010")
	t.Setenv("PENGUIN_V3_SEO_PAGE_MAX_AGE", "1m")
	var set ConfigSpec
	if err := envconfig.Process("penguin_v3", &set); err != nil {
		t.Fatal(err)
	}
	if !set.SEOPagesEnabled || set.SEOShellURL != "https://penguin-stats.io/" || set.SEOShellTTL != 2*time.Minute ||
		set.SEOShellFetchTimeout != time.Second || set.SEOSiteURL != "http://localhost:9010" || set.SEOPageMaxAge != time.Minute {
		t.Errorf("set: %+v", set)
	}
}
