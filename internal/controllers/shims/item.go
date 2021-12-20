package shims

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/utils"

	"github.com/ahmetb/go-linq/v3"
	"github.com/tidwall/gjson"

	"github.com/gofiber/fiber/v2"
)

type ItemController struct {
	repo *repos.ItemRepo
}

func RegisterItemController(v2 *server.V2, repo *repos.ItemRepo) {
	c := &ItemController{
		repo: repo,
	}

	v2.Get("/items", c.GetItems)
	v2.Get("/items/:itemId", c.GetItemByArkId)
}

func (c *ItemController) applyShim(item *shims.Item) {
	nameI18n := gjson.ParseBytes(item.NameI18n)
	item.Name = nameI18n.Map()["zh"].String()

	var coordSegments []int
	if item.Sprite != nil && item.Sprite.Valid {
		segments := strings.SplitN(item.Sprite.String, ":", 2)

		linq.From(segments).Select(func(i interface{}) interface{} {
			num, err := strconv.Atoi(i.(string))
			if err != nil {
				return -1
			}
			return num
		}).Where(func(i interface{}) bool {
			return i.(int) != -1
		}).ToSlice(&coordSegments)
	}
	if coordSegments != nil {
		item.SpriteCoord = &coordSegments
	}

	keywords := gjson.ParseBytes(item.Keywords)

	item.AliasMap = json.RawMessage(utils.Must(json.Marshal(keywords.Map()["alias"].Value().(map[string]interface{}))).([]byte))
	item.PronMap = json.RawMessage(utils.Must(json.Marshal(keywords.Map()["pron"].Value().(map[string]interface{}))).([]byte))
}

// @Summary      Get all Items
// @Tags         Item
// @Produce      json
// @Success      200     {array}  shims.Item{name_i18n=models.I18nString,existence=models.Existence}
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/v2/items [GET]
// @Deprecated
func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	items, err := c.repo.GetShimItems(ctx.Context())
	if err != nil {
		return err
	}

	for _, i := range items {
		c.applyShim(i)
	}

	return ctx.JSON(items)
}

// @Summary      Get an Item with ID
// @Tags         Item
// @Produce      json
// @Param        itemId  path      string  true  "Item ID"
// @Success      200     {object}  shims.Item{name_i18n=models.I18nString,existence=models.Existence}
// @Failure      400     {object}  errors.PenguinError "Invalid or missing itemId. Notice that this shall be the **string ID** of the item, instead of the internally used numerical ID of the item."
// @Failure      500     {object}  errors.PenguinError "An unexpected error occurred"
// @Router       /PenguinStats/v2/items/{itemId} [GET]
// @Deprecated
func (c *ItemController) GetItemByArkId(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	item, err := c.repo.GetShimItemByArkId(ctx.Context(), itemId)
	if err != nil {
		return err
	}

	c.applyShim(item)

	return ctx.JSON(item)
}
