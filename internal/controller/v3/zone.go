package v3

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ZoneController struct {
	fx.In

	ZoneService *service.Zone
}

func RegisterZone(v3 *svr.V3, c ZoneController) {
	v3.Get("/zones", c.GetZones)
	v3.Get("/zones/:zoneId", c.GetZoneById)
}

func (c *ZoneController) GetZones(ctx *fiber.Ctx) error {
	zones, err := c.ZoneService.GetZones(ctx.UserContext())
	if err != nil {
		return err
	}

	return ctx.JSON(zones)
}

func (c *ZoneController) GetZoneById(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.ZoneService.GetZoneByArkId(ctx.UserContext(), zoneId)
	if err != nil {
		return err
	}

	return ctx.JSON(zone)
}
