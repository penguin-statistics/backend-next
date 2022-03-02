package controllers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/models/gamedata"
	"github.com/penguin-statistics/backend-next/internal/models/types"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils/rekuest"
)

const TimeLayout = "2006-01-02 15:04:05 -07:00"

type AdminController struct {
	fx.In

	GamedataService *service.GamedataService
	AdminService    *service.AdminService
}

func RegisterAdminController(admin *svr.Admin, c AdminController) {
	admin.Post("/save", c.SaveRenderedObjects)
	admin.Post("/purge", c.PurgeCache)
}

func (c *AdminController) SaveRenderedObjects(ctx *fiber.Ctx) error {
	var request gamedata.RenderedObjects
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}

	err := c.AdminService.SaveRenderedObjects(ctx.Context(), &request)
	if err != nil {
		return err
	}

	return ctx.JSON(request)
}

func (c *AdminController) PurgeCache(ctx *fiber.Ctx) error {
	var request types.PurgeCacheRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}
	return cache.Delete(request.Name, request.Key)
}

// FIXME: Should be moved to a proper place
func (c *AdminController) UpdateNewEvent(ctx *fiber.Ctx) error {
	var request types.UpdateNewEventRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}

	startTime, endTime, err := getTimeFromString(request.TimeRange)
	if err != nil {
		return err
	}

	info := &gamedata.NewEventBasicInfo{
		ArkZoneID:    request.ArkZoneID,
		ZoneName:     request.ZoneName,
		ZoneCategory: request.ZoneCategory,
		ZoneType:     request.ZoneType,
		Server:       request.Server,
		StartTime:    startTime,
		EndTime:      endTime,
	}

	renderedObjects, err := c.GamedataService.UpdateNewEvent(ctx.Context(), info)
	if err != nil {
		return err
	}
	marshalResult, err := json.MarshalIndent(renderedObjects, "", "  ")
	if err != nil {
		return err
	}
	ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return ctx.Send(marshalResult)
}

func getTimeFromString(timeRange types.TimeRange) (*time.Time, *time.Time, error) {
	startTime, err := time.Parse(TimeLayout, timeRange.StartTime)
	if err != nil {
		return nil, nil, err
	}
	endTime := time.UnixMilli(constants.FakeEndTimeMilli)
	if timeRange.EndTime.Valid {
		endTime, err = time.Parse(TimeLayout, timeRange.EndTime.String)
		if err != nil {
			return nil, nil, err
		}
	}
	return &startTime, &endTime, nil
}
