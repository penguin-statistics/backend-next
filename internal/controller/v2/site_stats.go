package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util/rekuest"
)

type SiteStats struct {
	fx.In

	SiteStatsService *service.SiteStatsService
}

func RegisterSiteStats(v2 *svr.V2, c SiteStats) {
	v2.Get("/stats", c.GetSiteStats)
}

// @Summary      Get Site Stats
// @Tags         SiteStats
// @Produce      json
// @Param        server  query     string  true  "Server; default to CN" Enums(CN, US, JP, KR)
// @Success      200     {array}   v2.SiteStats
// @Failure      500     {object}  pgerr.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/stats [GET]
func (c *SiteStats) GetSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	siteStats, err := c.SiteStatsService.GetShimSiteStats(ctx.Context(), server)
	if err != nil {
		return err
	}

	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimSiteStats#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)

	return ctx.JSON(siteStats)
}
