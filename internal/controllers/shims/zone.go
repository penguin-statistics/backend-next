package shims

import (
	"github.com/gofiber/fiber/v2"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ZoneController struct {
	ZoneService *service.ZoneService
}

func RegisterZoneController(v2 *server.V2, zoneService *service.ZoneService) {
	c := &ZoneController{
		ZoneService: zoneService,
	}

	v2.Get("/zones", c.GetZones)
	v2.Get("/zones/:zoneId", c.GetZoneByArkId)
}

func (c *ZoneController) applyShim(zone *shims.Zone) {
	zoneNameI18n := gjson.ParseBytes(zone.ZoneNameI18n)
	zone.ZoneName = zoneNameI18n.Map()["zh"].String()

	if zone.Stages != nil {
		for _, stage := range zone.Stages {
			zone.StageIds = append(zone.StageIds, stage.ArkStageID)
		}
	}
}

// @Summary      Get All Zones
// @Tags         Zone
// @Produce      json
// @Success      200     {array}  shims.Zone{existence=models.Existence,zoneName_i18n=models.I18nString}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/zones [GET]
// @Deprecated
func (c *ZoneController) GetZones(ctx *fiber.Ctx) error {
	zones, err := c.ZoneService.GetShimZones(ctx)
	if err != nil {
		return err
	}

	for _, i := range zones {
		c.applyShim(i)
	}

	return ctx.JSON(zones)
}

// @Summary      Get a Zone with ID
// @Tags         Zone
// @Produce      json
// @Param        zoneId  path      int  true  "Zone ID"
// @Success      200     {object}  shims.Zone{existence=models.Existence,zoneName_i18n=models.I18nString}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing zoneId. Notice that this shall be the **string ID** of the zone, instead of the v3 API internally used numerical ID of the zone."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/zones/{zoneId} [GET]
// @Deprecated
func (c *ZoneController) GetZoneByArkId(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.ZoneService.GetShimZoneByArkId(ctx, zoneId)
	if err != nil {
		return err
	}

	c.applyShim(zone)

	return ctx.JSON(zone)
}
