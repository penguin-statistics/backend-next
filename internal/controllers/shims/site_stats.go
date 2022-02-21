package shims

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type SiteStatsController struct {
	service *service.SiteStatsService
}

func RegisterSiteStatsController(v2 *server.V2, s *service.SiteStatsService) {
	c := &SiteStatsController{
		service: s,
	}
	v2.Get("/stats", c.GetSiteStats)
}

// @Summary      Get Site Stats
// @Tags         SiteStats
// @Produce      json
// @Param        server  query      string  "CN"  "Server"
// @Success      200     {array}  shims.SiteStats
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/stats [GET]
// @Deprecated
func (c *SiteStatsController) GetSiteStats(ctx *fiber.Ctx) error {
	server := ctx.Query("server", "CN")

	siteStats, err := c.service.GetShimSiteStats(ctx.Context(), server)
	if err != nil {
		return err
	}

	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimSiteStats#server:"+server+"]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	ctx.Response().Header.SetLastModified(lastModifiedTime)

	return ctx.JSON(siteStats)
}
