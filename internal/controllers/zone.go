package controllers

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ZoneController struct {
	fx.In

	ZoneService *service.ZoneService
}

func RegisterZoneController(v3 *svr.V3, c ZoneController) {
	v3.Get("/zones", c.GetZones)
	v3.Get("/zones/:zoneId", c.GetZoneById)
}

// @Summary      Get All Zones
// @Tags         Zone
// @Produce      json
// @Success      200     {array}  models.Zone{existence=models.Existence,name=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/zones [GET]
func (c *ZoneController) GetZones(ctx *fiber.Ctx) error {
	zones, err := c.ZoneService.GetZones(ctx.Context())
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
func (c *ZoneController) GetZoneById(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.ZoneService.GetZoneByArkId(ctx.Context(), zoneId)
	if err != nil {
		return err
	}

	return ctx.JSON(zone)
}
