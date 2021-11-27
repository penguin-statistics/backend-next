package controllers

import (
	"database/sql"
	"net/http"
	"penguin-stats-v4/internal/models"
	"penguin-stats-v4/internal/pkg/errors"
	"penguin-stats-v4/internal/server"
	"penguin-stats-v4/internal/utils"
	"strings"

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
