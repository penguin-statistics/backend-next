package controller

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/util"
)

type ItemController struct {
	fx.In

	ItemService *service.ItemService
}

func RegisterItemController(v3 *svr.V3, c ItemController) {
	v3.Get("/items", c.GetItems)
	v3.Get("/items/:itemId", buildSanitizer(util.NonNullString, util.IsInt), c.GetItemById)
}

func buildSanitizer(sanitizer ...func(string) bool) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		itemId := strings.TrimSpace(ctx.Params("itemId"))

		for _, sanitizer := range sanitizer {
			if !sanitizer(itemId) {
				return pgerr.ErrInvalidReq.Msg("invalid or missing itemId")
			}
		}

		return ctx.Next()
	}
}

func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	items, err := c.ItemService.GetItems(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(items)
}

func (c *ItemController) GetItemById(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	item, err := c.ItemService.GetItemByArkId(ctx.Context(), itemId)
	if err != nil {
		return err
	}

	return ctx.JSON(item)
}
