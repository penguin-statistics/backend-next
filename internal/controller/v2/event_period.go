package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/core/activity"
	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
)

type EventPeriod struct {
	fx.In

	ActivityService *activity.Service
}

func RegisterEventPeriod(v2 *svr.V2, c EventPeriod) {
	v2.Get("/period", c.GetEventPeriods)
}

// @Summary  Get All Event Periods
// @Tags     EventPeriod
// @Produce  json
// @Success  200  {array}   modelv2.Activity{label_i18n=model.I18nString,existence=model.Existence}
// @Failure  500  {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/period [GET]
func (c *EventPeriod) GetEventPeriods(ctx *fiber.Ctx) (err error) {
	var activities []*modelv2.Activity
	activities, err = c.ActivityService.GetShimActivities(ctx.Context())
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimActivities]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)
	return ctx.JSON(activities)
}
