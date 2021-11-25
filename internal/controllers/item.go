package controllers

import (
	"penguin-stats-v4/internal/models"
	"penguin-stats-v4/internal/server"
	"penguin-stats-v4/internal/utils"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

type ItemController struct {
	db *bun.DB
}

func RegisterItemController(v2 *server.V2, v3 *server.V3, db *bun.DB) {
	c := &ItemController{
		db: db,
	}

	v2.Get("/items", c.GetItems)
	v2.Get("/items/:itemId", buildSanitizer(utils.NonNullString, utils.IsValidId), c.GetItemByArkId)

	v3.Get("/items/:itemId", buildSanitizer(utils.NonNullString, utils.IsInt), c.GetItemById)
}

func buildSanitizer(sanitizer ...func(string) bool) func(ctx *fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		itemId := strings.TrimSpace(ctx.Params("itemId"))

		for _, sanitizer := range sanitizer {
			if !sanitizer(itemId) {
				return utils.RespondBadRequest(ctx, "invalid or missing itemId")
			}
		}

		return ctx.Next()
	}
}

func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	var items []models.PItem

	err := c.db.NewSelect().Model(&items).Scan(ctx.Context())
	if err != nil {
		return err
	}

	return ctx.JSON(items)
}

func (c *ItemController) GetItemById(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	var item models.PItem
	err := c.db.NewSelect().
		Model(&item).
		Where("id = ?", itemId).
		Scan(ctx.Context())

	if err != nil {
		return err
	}

	return ctx.JSON(item)
}

func (c *ItemController) GetItemByArkId(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	var item models.PItem
	err := c.db.NewSelect().
		Model(&item).
		Where("ark_item_id = ?", itemId).
		Scan(ctx.Context())

	if err != nil {
		return err
	}

	return ctx.JSON(item)
}
