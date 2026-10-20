package v2

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	modelv2 "exusiai.dev/backend-next/internal/model/v2"
	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

var _ modelv2.Dummy

type Zone struct {
	fx.In

	ZoneService *service.Zone
}

func RegisterZone(v2 *svr.V2, c Zone) {
	v2.Get("/zones", c.GetZones)
	v2.Get("/zones/:zoneId", c.GetZoneByArkId)
}

//	@Summary	Get All Zones
//	@Tags		Zone
//	@Produce	json
//	@Success	200	{array}		modelv2.Zone{existence=model.Existence,zoneName_i18n=model.I18nString}
//	@Failure	500	{object}	pgerr.PenguinError	"An unexpected error occurred"
//	@Router		/PenguinStats/api/v2/zones [GET]
func (c *Zone) GetZones(ctx *fiber.Ctx) error {
	lastModified := lastModifiedTime("[shimZones]")
	zones, err := c.ZoneService.GetShimZones(ctx.UserContext())
	if err != nil {
		return err
	}
	return sendCacheable(ctx, lastModified, zones)
}

//	@Summary	Get a Zone with ID
//	@Tags		Zone
//	@Produce	json
//	@Param		zoneId	path		int	true	"Zone ID"
//	@Success	200		{object}	modelv2.Zone{existence=model.Existence,zoneName_i18n=model.I18nString}
//	@Failure	400		{object}	pgerr.PenguinError	"Invalid or missing zoneId. Notice that this shall be the **string ID** of the zone, instead of the internally used numerical ID of the zone."
//	@Failure	500		{object}	pgerr.PenguinError	"An unexpected error occurred"
//	@Router		/PenguinStats/api/v2/zones/{zoneId} [GET]
func (c *Zone) GetZoneByArkId(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.ZoneService.GetShimZoneByArkId(ctx.UserContext(), zoneId)
	if err != nil {
		return err
	}
	return ctx.JSON(zone)
}
