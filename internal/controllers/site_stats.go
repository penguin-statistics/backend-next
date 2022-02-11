package controllers

import (
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server"
)

type SiteStatsController struct {
	fx.In
}

func RegisterSiteStatsController(v3 *server.V3) {
}
