package controllers

import (
	"net/http"
	"strings"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/fx"

	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type ItemController struct {
	fx.In

	ItemService *service.ItemService
	Redis       *redis.Client
}

func RegisterItemController(v3 *svr.V3, c ItemController) {
	v3.Get("/items", c.GetItems)
	v3.Get("/items/:itemId", buildSanitizer(utils.NonNullString, utils.IsInt), c.GetItemById)
}

func buildSanitizer(sanitizer ...func(string) bool) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		itemId := strings.TrimSpace(ctx.Params("itemId"))

		for _, sanitizer := range sanitizer {
			if !sanitizer(itemId) {
				return fiber.NewError(http.StatusBadRequest, "invalid or missing itemId")
			}
		}

		return ctx.Next()
	}
}

// @Summary      Get All Items
// @Tags         Item
// @Produce      json
// @Success      200     {array}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/items [GET]
func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	items, err := c.ItemService.GetItems(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(items)
}

// @Summary      Get an Item with ID
// @Tags         Item
// @Produce      json
// @Param        itemId  path      string  true  "Item ID"
// @Success      200     {object}  models.Item{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/items/{itemId} [GET]
func (c *ItemController) GetItemById(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	item, err := c.ItemService.GetItemByArkId(ctx.Context(), itemId)
	if err != nil {
		return err
	}

	return ctx.JSON(item)
}
