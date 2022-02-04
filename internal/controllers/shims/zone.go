package shims

import (
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"

	"github.com/tidwall/gjson"

	"github.com/gofiber/fiber/v2"
)

type ZoneController struct {
	repo *repos.ZoneRepo
}

func RegisterZoneController(v2 *server.V2, repo *repos.ZoneRepo) {
	c := &ZoneController{
		repo: repo,
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
	zones, err := c.repo.GetShimZones(ctx.Context())
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

	zone, err := c.repo.GetShimZoneByArkId(ctx.Context(), zoneId)
	if err != nil {
		return err
	}

	c.applyShim(zone)

	return ctx.JSON(zone)
}
