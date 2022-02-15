package shims

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/models/cache"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type NoticeController struct {
	service *service.NoticeService
}

func RegisterNoticeController(v2 *server.V2, service *service.NoticeService) {
	c := &NoticeController{
		service: service,
	}

	v2.Get("/notice", c.GetNotices)
}

// @Summary      Get All Notices
// @Tags         Notice
// @Produce      json
// @Success      200     {array}  models.Notice
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/notice [GET]
// @Deprecated
func (c *NoticeController) GetNotices(ctx *fiber.Ctx) error {
	notices, err := c.service.GetNotices(ctx)
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[notices]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	ctx.Response().Header.SetLastModified(lastModifiedTime)
	return ctx.JSON(notices)
}
