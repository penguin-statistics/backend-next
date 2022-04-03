package v2

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/model/cache"
	"github.com/penguin-statistics/backend-next/internal/pkg/cachectrl"
	"github.com/penguin-statistics/backend-next/internal/server/svr"
	"github.com/penguin-statistics/backend-next/internal/service"
)

type ItemController struct {
	ItemService *service.ItemService
}

func RegisterItemController(v2 *svr.V2, itemService *service.ItemService) {
	c := &ItemController{
		ItemService: itemService,
	}

	v2.Get("/items", c.GetItems)
	v2.Get("/items/:itemId", c.GetItemByArkId)
}

// @Summary      Get All Items
// @Tags         Item
// @Produce      json
// @Success      200     {array}  v2.Item{name_i18n=model.I18nString,existence=model.Existence}
// @Failure      500     {object}  pgerr.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/items [GET]
func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	items, err := c.ItemService.GetShimItems(ctx.Context())
	if err != nil {
		return err
	}
	var lastModifiedTime time.Time
	if err := cache.LastModifiedTime.Get("[shimItems]", &lastModifiedTime); err != nil {
		lastModifiedTime = time.Now()
	}
	cachectrl.OptIn(ctx, lastModifiedTime)
	return ctx.JSON(items)
}

// @Summary      Get an Item with ID
// @Tags         Item
// @Produce      json
// @Param        itemId  path      string  true  "Item ID"
// @Success      200     {object}  v2.Item{name_i18n=model.I18nString,existence=model.Existence}
// @Failure      400     {object}  pgerr.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  pgerr.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/api/v2/items/{itemId} [GET]
func (c *ItemController) GetItemByArkId(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	item, err := c.ItemService.GetShimItemByArkId(ctx.Context(), itemId)
	if err != nil {
		return err
	}
	return ctx.JSON(item)
}
