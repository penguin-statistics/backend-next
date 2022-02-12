package service

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/shims"
	"github.com/penguin-statistics/backend-next/internal/repos"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type ItemService struct {
	ItemRepo *repos.ItemRepo
}

func NewItemService(itemRepo *repos.ItemRepo) *ItemService {
	return &ItemService{
		ItemRepo: itemRepo,
	}
}

func (s *ItemService) GetItems(ctx *fiber.Ctx) ([]*models.Item, error) {
	return s.ItemRepo.GetItems(ctx.Context())
}

func (s *ItemService) GetItemById(ctx *fiber.Ctx, itemId int) (*models.Item, error) {
	return s.ItemRepo.GetItemById(ctx.Context(), itemId)
}

func (s *ItemService) GetItemByArkId(ctx *fiber.Ctx, arkItemId string) (*models.Item, error) {
	return s.ItemRepo.GetItemByArkId(ctx.Context(), arkItemId)
}

func (s *ItemService) GetShimItems(ctx *fiber.Ctx) ([]*shims.Item, error) {
	items, err := s.ItemRepo.GetShimItems(ctx.Context())
	if err != nil {
		return nil, err
	}
	for _, i := range items {
		s.applyShim(i)
	}
	return items, nil
}

func (s *ItemService) GetShimItemByArkId(ctx *fiber.Ctx, itemId string) (*shims.Item, error) {
	item, err := s.ItemRepo.GetShimItemByArkId(ctx.Context(), itemId)
	if err != nil {
		return nil, err
	}
	s.applyShim(item)
	return item, nil
}

func (s *ItemService) applyShim(item *shims.Item) {
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

	item.AliasMap = json.RawMessage(utils.Must(json.Marshal(keywords.Get("alias").Value().(map[string]interface{}))).([]byte))
	item.PronMap = json.RawMessage(utils.Must(json.Marshal(keywords.Get("pron").Value().(map[string]interface{}))).([]byte))
}
