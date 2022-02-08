package controllers

import (
	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
)

type SiteStatsController struct {
	fx.In

	Repo  *repos.ZoneRepo
	Redis *redis.Client
}

func RegisterSiteStatsController(v3 *server.V3, c SiteStatsController) {
	v3.Get("/stats", c.GetSiteStats)
}

// @Summary      Get All Zones
// @Tags         Zone
// @Produce      json
// @Success      200     {array}  models.Zone{existence=models.Existence,name=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/zones [GET]
func (c *SiteStatsController) GetSiteStats(ctx *fiber.Ctx) error {
	zones, err := c.Repo.GetZones(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(zones)
}

// @Summary      Get a Zone with ID
// @Tags         Zone
// @Produce      json
// @Param        zoneId  path      int  true  "Zone ID"
// @Success      200     {object}  models.Zone{existence=models.Existence,name=models.I18nString}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing zoneId. Notice that this shall be the **string ID** of the zone, instead of the internally used numerical ID of the zone."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/zones/{zoneId} [GET]
func (c *SiteStatsController) GetZoneById(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.Repo.GetZoneByArkId(ctx.Context(), zoneId)
	if err != nil {
		return err
	}

	return ctx.JSON(zone)
}
