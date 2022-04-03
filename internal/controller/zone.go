package controller

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

func (c *ZoneController) GetZones(ctx *fiber.Ctx) error {
	zones, err := c.ZoneService.GetZones(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(zones)
}

func (c *ZoneController) GetZoneById(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.ZoneService.GetZoneByArkId(ctx.Context(), zoneId)
	if err != nil {
		return err
	}

	return ctx.JSON(zone)
}
