package service

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/models"
	"github.com/penguin-statistics/backend-next/internal/models/cache"
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

// Cache: items, 24hrs
func (s *ItemService) GetItems(ctx *fiber.Ctx) ([]*models.Item, error) {
	var items []*models.Item
	err := cache.Items.Get("", items)
	if err == nil {
		return items, nil
	}

	items, err = s.ItemRepo.GetItems(ctx.Context())
	if err != nil {
		return nil, err
	}
	go cache.Items.Set("", items, 24*time.Hour)
	return items, nil
}

// Cache: item#itemId:{itemId}, 24hrs
func (s *ItemService) GetItemById(ctx *fiber.Ctx, itemId int) (*models.Item, error) {
	var item models.Item
	err := cache.ItemById.Get(strconv.Itoa(itemId), &item)
	if err == nil {
		log.Debug().Msgf("got cache content from cache for %d", itemId)
		return &item, nil
	}
	log.Debug().Err(err).Msgf("no cache content for %d", itemId)

	dbItem, err := s.ItemRepo.GetItemById(ctx.Context(), itemId)
	if err != nil {
		return nil, err
	}
	go cache.ItemById.Set(strconv.Itoa(itemId), item, 24*time.Hour)
	return dbItem, nil
}

// Cache: item#arkItemId:{arkItemId}, 24hrs
func (s *ItemService) GetItemByArkId(ctx *fiber.Ctx, arkItemId string) (*models.Item, error) {
	var item *models.Item
	err := cache.ItemByArkId.Get(arkItemId, item)
	if err == nil {
		return item, nil
	}

	item, err = s.ItemRepo.GetItemByArkId(ctx.Context(), arkItemId)
	if err != nil {
		return nil, err
	}
	go cache.ItemByArkId.Set(arkItemId, item, 24*time.Hour)
	return item, nil
}

// Cache: shimItems, 24hrs, record last modified time
func (s *ItemService) GetShimItems(ctx *fiber.Ctx) ([]*shims.Item, error) {
	var items []*shims.Item
	err := cache.ShimItems.Get("", items)
	if err == nil {
		return items, nil
	}

	items, err = s.ItemRepo.GetShimItems(ctx.Context())
	if err != nil {
		return nil, err
	}
	for _, i := range items {
		s.applyShim(i)
	}
	go cache.ShimItems.Set("", items, 24*time.Hour)
	go cache.LastModifiedTime.Set("[shimItems]", time.Now().UnixMilli(), 0)
	return items, nil
}

// Cache: shimItem#arkItemId:{arkItemId}, 24hrs
func (s *ItemService) GetShimItemByArkId(ctx *fiber.Ctx, arkItemId string) (*shims.Item, error) {
	var item *shims.Item
	err := cache.ShimItemByArkId.Get(arkItemId, item)
	if err == nil {
		return item, nil
	}

	item, err = s.ItemRepo.GetShimItemByArkId(ctx.Context(), arkItemId)
	if err != nil {
		return nil, err
	}
	s.applyShim(item)
	go cache.ShimItemByArkId.Set(arkItemId, item, 24*time.Hour)
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
