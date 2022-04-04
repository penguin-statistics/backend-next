package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
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

// @Summary  Get All Zones
// @Tags     Zone
// @Produce  json
// @Success  200  {array}   modelv2.Zone{existence=model.Existence,zoneName_i18n=model.I18nString}
// @Failure  500  {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/zones [GET]
func (c *Zone) GetZones(ctx *fiber.Ctx) error {
	zones, err := c.ZoneService.GetShimZones(ctx.Context())
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimZones]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)
	return ctx.JSON(zones)
}

// @Summary  Get a Zone with ID
// @Tags     Zone
// @Produce  json
// @Param    zoneId  path      int  true  "Zone ID"
// @Success  200     {object}  modelv2.Zone{existence=model.Existence,zoneName_i18n=model.I18nString}
// @Failure  400     {object}  pgerr.PenguinError  "Invalid or missing zoneId. Notice that this shall be the **string ID** of the zone, instead of the v3 API internally used numerical ID of the zone."
// @Failure  500     {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/zones/{zoneId} [GET]
func (c *Zone) GetZoneByArkId(ctx *fiber.Ctx) error {
	zoneId := ctx.Params("zoneId")

	zone, err := c.ZoneService.GetShimZoneByArkId(ctx.Context(), zoneId)
	if err != nil {
		return err
	}
	return ctx.JSON(zone)
}
