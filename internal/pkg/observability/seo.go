package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	SEOShellFetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "seo", "shell_fetch_total"),
		Help: "Fetches of the frontend's index.html for the stats pages by outcome (ok, ok_unspliceable, not_modified, error)",
	}, []string{"outcome"})
	SEOPageRenderTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: prometheus.BuildFQName(ServiceName, "seo", "page_render_total"),
		Help: "Stats pages served by page (item, item_list, stage, stage_list, other) and outcome " +
			"(ok, no_data, noindex, not_found, redirect, shell_only, error, unavailable_redirect, unavailable)",
	}, []string{"page", "outcome"})
)
