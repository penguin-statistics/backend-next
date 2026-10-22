package observability

import (
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// seoShellFreshAt is when the frontend's index.html was last fetched or revalidated, or when fetching it started
// before that, in Unix nanoseconds; 0 while the stats pages are off
var seoShellFreshAt atomic.Int64

var (
	SEOShellFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "seo", "shell_fetch_total"),
		Help: "Fetches of the frontend's index.html for the stats pages by outcome (ok, ok_unspliceable, not_modified, error)",
	}, []string{"outcome"})
	SEOShellAgeSeconds = promauto.NewGaugeFunc(prometheus.GaugeOpts{
		Name: prometheus.BuildFQName(ServiceName, "seo", "shell_age_seconds"),
		Help: "Seconds since the frontend's index.html served in the stats pages was last fetched or revalidated " +
			"(since fetching started, if it never was); 0 while the stats pages are off",
	}, func() float64 {
		t := seoShellFreshAt.Load()
		if t == 0 {
			return 0
		}
		return time.Since(time.Unix(0, t)).Seconds()
	})
	SEOPageRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "seo", "page_render_total"),
		Help: "Stats pages and their sitemaps served by page (item, item_list, stage, stage_list, other, " +
			"sitemap_index, sitemap) and outcome (ok, no_data, noindex, cold, not_found, redirect, shell_only, error, " +
			"unavailable_redirect, unavailable, disabled_redirect, not_modified, disabled)",
	}, []string{"page", "outcome"})
)

// SEOShellFresh records that the frontend's index.html was fetched or revalidated at t, or that fetching it started.
func SEOShellFresh(t time.Time) {
	seoShellFreshAt.Store(t.UnixNano())
}
