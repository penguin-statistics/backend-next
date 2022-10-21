package v2

import (
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"exusiai.dev/backend-next/internal/server/svr"
	"exusiai.dev/backend-next/internal/service"
)

type FrontendConfig struct {
	fx.In

	FrontendConfigService *service.FrontendConfig
}

func RegisterFrontendConfig(v2 *svr.V2, c FrontendConfig) {
	v2.Get("/config", c.GetFrontendConfig)
}

// @Summary  Get FrontendConfig
// @Tags     FrontendConfig
// @Produce  json
// @Success  200
// @Failure  500  {object}  pgerr.PenguinError  "An unexpected error occurred"
// @Router   /PenguinStats/api/v2/config [GET]
func (c *FrontendConfig) GetFrontendConfig(ctx *fiber.Ctx) error {
	formula, err := c.FrontendConfigService.GetFrontendConfig(ctx.UserContext())
	if err != nil {
		return err
	}
	return ctx.JSON(formula)
}
