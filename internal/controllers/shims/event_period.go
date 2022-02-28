package shims

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type EventPeriodController struct {
	fx.In

	ActivityService *service.ActivityService
}

func RegisterEventPeriodController(v2 *svr.V2, c EventPeriodController) {
	v2.Get("/period", c.GetEventPeriods)
}

// @Summary      Get All Event Periods
// @Tags         EventPeriod
// @Produce      json
// @Success      200     {array}  shims.Activity{label_i18n=models.I18nString,existence=models.Existence}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/period [GET]
// @Deprecated
func (c *EventPeriodController) GetEventPeriods(ctx *fiber.Ctx) error {
	activities, err := c.ActivityService.GetShimActivities(ctx.Context())
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimActivities]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	ctx.Response().Header.SetLastModified(lastModifiedTime)
	return ctx.JSON(activities)
}
