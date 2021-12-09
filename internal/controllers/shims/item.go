package shims

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/server"
	"github.com/penguin-statistics/backend-next/internal/utils"
	"github.com/penguin-statistics/fiberotel"

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

func applyShim(item *shims.PItem) {
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

func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	items, err := c.repo.GetShimItems(fiberotel.FromCtx(ctx))
	if err != nil {
		return err
	}

	_, span := fiberotel.StartTracerFromCtx(ctx, "applyShims")
	for _, i := range items {
		applyShim(i)
	}
	span.End()

	return ctx.JSON(items)
}

func (c *ItemController) GetItemByArkId(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	item, err := c.repo.GetShimItemById(ctx.Context(), itemId)
	if err != nil {
		return err
	}

	applyShim(item)

	return ctx.JSON(item)
}
