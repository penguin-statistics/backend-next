package shims

import (
	"encoding/json"
	"github.com/ahmetb/go-linq/v3"
	"github.com/tidwall/gjson"
	"penguin-stats-v4/internal/models/helpers"
	"penguin-stats-v4/internal/models/shims"
	"penguin-stats-v4/internal/server"
	"penguin-stats-v4/internal/utils"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

type ItemController struct {
	db *bun.DB
}

func RegisterItemController(v2 *server.V2, db *bun.DB) {
	c := &ItemController{
		db: db,
	}

	v2.Get("/items", c.GetItems)
	v2.Get("/items/:itemId", c.GetItemByArkId)
}

func applyShim(item *shims.PItem) {
	nameI18n := gjson.ParseBytes(item.NameI18n)
	item.Name = nameI18n.Map()["zh"].String()

	var coordSegments []int
	if item.Sprite.Valid {
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
	item.SpriteCoords = helpers.NewIntSlice(coordSegments, coordSegments != nil)

	keywords := gjson.ParseBytes(item.Keywords)

	item.AliasMap = json.RawMessage(utils.Must(json.Marshal(keywords.Map()["alias"].Value().(map[string]interface{}))).([]byte))
	item.PronMap = json.RawMessage(utils.Must(json.Marshal(keywords.Map()["pron"].Value().(map[string]interface{}))).([]byte))
}

func (c *ItemController) GetItems(ctx *fiber.Ctx) error {
	var items []*shims.PItem

	err := c.db.NewSelect().Model(&items).Scan(ctx.Context())
	if err != nil {
		return err
	}

	for _, i := range items {
		applyShim(i)
	}

	return ctx.JSON(items)
}

func (c *ItemController) GetItemByArkId(ctx *fiber.Ctx) error {
	itemId := ctx.Params("itemId")

	var item shims.PItem
	err := c.db.NewSelect().
		Model(&item).
		Where("ark_item_id = ?", itemId).
		Scan(ctx.Context())

	if err != nil {
		return err
	}

	applyShim(&item)

	return ctx.JSON(item)
}
