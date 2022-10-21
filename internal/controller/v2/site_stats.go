package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/model/cache"
	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/pkg/cachectrl"
	"exusiai.dev/backend-next/internal/pkg/middlewares"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
	"exusiai.dev/backend-next/internal/util/rekuest"
)

var _ modelv2.Dummy

type SiteStats struct {
	fx.In

	SiteStatsService *service.SiteStats
}

func RegisterSiteStats(v2 *svr.V2, c SiteStats) {
	v2.Get("/stats", middlewares.ValidateServerAsQuery, c.GetSiteStats)
}

// @Summary  Get Site Stats
// @Tags     SiteStats
// @Produce  json
// @Param    server  query     string  true  "Server; default to CN"  Enums(CN, US, JP, KR)
// @Success  200     {array}   modelv2.SiteStats
// @Failure  500     {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/stats [GET]
func (c *SiteStats) GetSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")
	if err := rekuest.ValidServer(ctx, server); err != nil {
		return err
	}

	siteStats, err := c.SiteStatsService.GetShimSiteStats(ctx.UserContext(), server)
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
