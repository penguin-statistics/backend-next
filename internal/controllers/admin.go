package controllers

import (
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/constants"
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
	admin.Post("/render/event", c.UpdateNewEvent)
	admin.Post("/save", c.SaveRenderedObjects)
}

func (c *AdminController) UpdateNewEvent(ctx *fiber.Ctx) error {
	var request types.UpdateNewEventRequest
	if err := rekuest.ValidBody(ctx, &request); err != nil {
		return err
	}

	startTime, err := time.Parse(TimeLayout, request.StartTime)
	if err != nil {
		return err
	}
	endTime := time.UnixMilli(constants.FakeEndTimeMilli)
	if request.EndTime.Valid {
		endTime, err = time.Parse(TimeLayout, request.EndTime.String)
		if err != nil {
			return err
		}
	}

	info := &gamedata.NewEventBasicInfo{
		ArkZoneID:    request.ArkZoneID,
		ZoneName:     request.ZoneName,
		ZoneCategory: request.ZoneCategory,
		ZoneType:     request.ZoneType,
		Server:       request.Server,
		StartTime:    &startTime,
		EndTime:      &endTime,
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
