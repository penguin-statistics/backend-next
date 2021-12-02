package controllers

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/pkg/errors"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/utils"

	"github.com/go-redis/redis/v8"
	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

type ItemController struct {
	db    *bun.DB
	redis *redis.Client
}

func RegisterItemController(v3 *server.V3, db *bun.DB, redis *redis.Client) {
	c := &ItemController{
		db:    db,
		redis: redis,
	}

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

// GetItemById godoc
// @Summary      Gets an Item using numerical ID
// @Description  Gets an Item using the item's numerical ID
// @Tags         Item
// @Produce      json
// @Param        itemId  path      int  true  "Numerical Item ID"
// @Success      200     {object}  models.PItem{name=models.I18nString,existence=models.Existence,keywords=models.Keywords}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **numerical ID** of the item, instead of the previously used string form **arkItemId** of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /v3/items/{itemId} [GET]
func (c *ItemController) GetItemById(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	var item models.PItem
	err := c.db.NewSelect().
		Model(&item).
		Where("id = ?", itemId).
		Scan(ctx.Context())

	if err == sql.ErrNoRows {
		return errors.ErrNotFound
	}

	if err != nil {
		return err
	}

	return ctx.JSON(item)
}
