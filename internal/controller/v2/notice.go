package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type Notice struct {
	fx.In

	NoticeService *service.Notice
}

func RegisterNotice(v2 *svr.V2, c Notice) {
	v2.Get("/notice", c.GetNotices)
}

// @Summary  Get All Notices
// @Tags     Notice
// @Produce  json
// @Success  200  {array}   model.Notice
// @Failure  500  {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/notice [GET]
func (c *Notice) GetNotices(ctx *fiber.Ctx) error {
	notices, err := c.NoticeService.GetNotices(ctx.Context())
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[notices]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)
	return ctx.JSON(notices)
}
