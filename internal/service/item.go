package service

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
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
func (s *ItemService) GetItems(ctx context.Context) ([]*models.Item, error) {
	var items []*models.Item
	err := cache.Items.Get(&items)
	if err == nil {
		return items, nil
	}

	items, err = s.ItemRepo.GetItems(ctx)
	if err != nil {
		return nil, err
	}
	go cache.Items.Set(items, 24*time.Hour)
	return items, nil
}

func (s *ItemService) GetItemById(ctx context.Context, itemId int) (*models.Item, error) {
	itemsMapById, err := s.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}
	item, ok := itemsMapById[itemId]
	if !ok {
		return nil, nil
	}
	return item, nil
}

// Cache: item#arkItemId:{arkItemId}, 24hrs
func (s *ItemService) GetItemByArkId(ctx context.Context, arkItemId string) (*models.Item, error) {
	var item models.Item
	err := cache.ItemByArkId.Get(arkItemId, &item)
	if err == nil {
		return &item, nil
	}

	dbItem, err := s.ItemRepo.GetItemByArkId(ctx, arkItemId)
	if err != nil {
		return nil, err
	}
	go cache.ItemByArkId.Set(arkItemId, dbItem, 24*time.Hour)
	return dbItem, nil
}

func (s *ItemService) SearchItemByName(ctx context.Context, name string) (*models.Item, error) {
	return s.ItemRepo.SearchItemByName(ctx, name)
}

// Cache: (singular) shimItems, 24hrs; records last modified time
func (s *ItemService) GetShimItems(ctx context.Context) ([]*shims.Item, error) {
	var items []*shims.Item
	err := cache.ShimItems.Get(&items)
	if err == nil {
		return items, nil
	}

	items, err = s.ItemRepo.GetShimItems(ctx)
	if err != nil {
		return nil, err
	}
	for _, i := range items {
		s.applyShim(i)
	}
	if err := cache.ShimItems.Set(items, 24*time.Hour); err == nil {
		cache.LastModifiedTime.Set("[shimItems]", time.Now(), 0)
	}
	return items, nil
}

// Cache: shimItem#arkItemId:{arkItemId}, 24hrs
func (s *ItemService) GetShimItemByArkId(ctx context.Context, arkItemId string) (*shims.Item, error) {
	var item shims.Item
	err := cache.ShimItemByArkId.Get(arkItemId, &item)
	if err == nil {
		return &item, nil
	}

	dbItem, err := s.ItemRepo.GetShimItemByArkId(ctx, arkItemId)
	if err != nil {
		return nil, err
	}
	s.applyShim(dbItem)
	go cache.ShimItemByArkId.Set(arkItemId, dbItem, 24*time.Hour)
	return dbItem, nil
}

// Cache: (singular) itemsMapById, 24hrs
func (s *ItemService) GetItemsMapById(ctx context.Context) (map[int]*models.Item, error) {
	var itemsMapById map[int]*models.Item
	cache.ItemsMapById.MutexGetSet(&itemsMapById, func() (interface{}, error) {
		items, err := s.GetItems(ctx)
		if err != nil {
			return nil, err
		}
		itemsMapById := make(map[int]*models.Item)
		for _, item := range items {
			itemsMapById[item.ItemID] = item
		}
		return itemsMapById, nil
	}, 24*time.Hour)
	return itemsMapById, nil
}

// Cache: (singular) itemsMapByArkId, 24hrs
func (s *ItemService) GetItemsMapByArkId(ctx context.Context) (map[string]*models.Item, error) {
	var itemsMapByArkId map[string]*models.Item
	cache.ItemsMapByArkId.MutexGetSet(&itemsMapByArkId, func() (interface{}, error) {
		items, err := s.GetItems(ctx)
		if err != nil {
			return nil, err
		}
		itemsMapByArkId := make(map[string]*models.Item)
		for _, item := range items {
			itemsMapByArkId[item.ArkItemID] = item
		}
		return itemsMapByArkId, nil
	}, 24*time.Hour)
	return itemsMapByArkId, nil
}

func (s *ItemService) applyShim(item *shims.Item) {
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
	if coordSegments != nil {
		item.SpriteCoord = &coordSegments
	}

	keywords := gjson.ParseBytes(item.Keywords)

	item.AliasMap = json.RawMessage(utils.Must(json.Marshal(keywords.Get("alias").Value().(map[string]interface{}))).([]byte))
	item.PronMap = json.RawMessage(utils.Must(json.Marshal(keywords.Get("pron").Value().(map[string]interface{}))).([]byte))
}
