package v3

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type InitController struct {
	fx.In

	InitService *service.Init
}

func RegisterInit(v3 *svr.V3, c InitController) {
	v3.Get("/init", c.Init)
}

func (c *InitController) Init(ctx *fiber.Ctx) error {
	inits, err := c.InitService.GetInit(ctx.UserContext())
	if err != nil {
		return err
	}

	return ctx.JSON(inits)
}
