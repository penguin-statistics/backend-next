package item

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ahmetb/go-linq/v3"
	"github.com/tidwall/gjson"

	"github.com/penguin-statistics/backend-next/internal/model/cache"
	modelv2 "github.com/penguin-statistics/backend-next/internal/model/v2"
	"github.com/penguin-statistics/backend-next/internal/pkg/pgerr"
	"github.com/penguin-statistics/backend-next/internal/util"
)

type Service struct {
	ItemRepo *Repo
}

func NewService(itemRepo *Repo) *Service {
	return &Service{
		ItemRepo: itemRepo,
	}
}

// Cache: items, 24hrs
func (s *Service) GetItems(ctx context.Context) ([]*Model, error) {
	var items []*Model
	err := CachedItems.Get(&items)
	if err == nil {
		return items, nil
	}

	items, err = s.ItemRepo.GetItems(ctx)
	if err != nil {
		return nil, err
	}
	go CachedItems.Set(items, 24*time.Hour)
	return items, nil
}

func (s *Service) GetItemById(ctx context.Context, itemId int) (*Model, error) {
	itemsMapById, err := s.GetItemsMapById(ctx)
	if err != nil {
		return nil, err
	}
	item, ok := itemsMapById[itemId]
	if !ok {
		return nil, pgerr.ErrNotFound
	}
	return item, nil
}

// Cache: item#arkItemId:{arkItemId}, 24hrs
func (s *Service) GetItemByArkId(ctx context.Context, arkItemId string) (*Model, error) {
	var item Model
	err := CachedItemByArkID.Get(arkItemId, &item)
	if err == nil {
		return &item, nil
	}

	dbItem, err := s.ItemRepo.GetItemByArkId(ctx, arkItemId)
	if err != nil {
		return nil, err
	}
	go CachedItemByArkID.Set(arkItemId, *dbItem, 24*time.Hour)
	return dbItem, nil
}

func (s *Service) SearchItemByName(ctx context.Context, name string) (*Model, error) {
	return s.ItemRepo.SearchItemByName(ctx, name)
}

// Cache: (singular) shimItems, 24hrs; records last modified time
func (s *Service) GetShimItems(ctx context.Context) ([]*modelv2.Item, error) {
	var items []*modelv2.Item
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
func (s *Service) GetShimItemByArkId(ctx context.Context, arkItemId string) (*modelv2.Item, error) {
	var item modelv2.Item
	err := cache.ShimItemByArkID.Get(arkItemId, &item)
	if err == nil {
		return &item, nil
	}

	dbItem, err := s.ItemRepo.GetShimItemByArkId(ctx, arkItemId)
	if err != nil {
		return nil, err
	}
	s.applyShim(dbItem)
	go cache.ShimItemByArkID.Set(arkItemId, *dbItem, 24*time.Hour)
	return dbItem, nil
}

// Cache: (singular) itemsMapById, 24hrs
func (s *Service) GetItemsMapById(ctx context.Context) (map[int]*Model, error) {
	var itemsMapById map[int]*Model
	CachedItemsMapById.MutexGetSet(&itemsMapById, func() (map[int]*Model, error) {
		items, err := s.GetItems(ctx)
		if err != nil {
			return nil, err
		}
		s := make(map[int]*Model)
		for _, item := range items {
			s[item.ItemID] = item
		}
		return s, nil
	}, 24*time.Hour)
	return itemsMapById, nil
}

// Cache: (singular) itemsMapByArkId, 24hrs
func (s *Service) GetItemsMapByArkId(ctx context.Context) (map[string]*Model, error) {
	var itemsMapByArkId map[string]*Model
	CachedItemsMapByArkID.MutexGetSet(&itemsMapByArkId, func() (map[string]*Model, error) {
		items, err := s.GetItems(ctx)
		if err != nil {
			return nil, err
		}
		s := make(map[string]*Model)
		for _, item := range items {
			s[item.ArkItemID] = item
		}
		return s, nil
	}, 24*time.Hour)
	return itemsMapByArkId, nil
}

func (s *Service) applyShim(item *modelv2.Item) {
	nameI18n := gjson.ParseBytes(item.NameI18n)
	item.Name = nameI18n.Map()["zh"].String()

	var coordSegments []int
	if item.Sprite.Valid {
		segments := strings.SplitN(item.Sprite.String, ":", 2)

		linq.From(segments).Select(func(i any) any {
			num, err := strconv.Atoi(i.(string))
			if err != nil {
				return -1
			}
			return num
		}).Where(func(i any) bool {
			return i.(int) != -1
		}).ToSlice(&coordSegments)
	}
	if coordSegments != nil {
		item.SpriteCoord = &coordSegments
	}

	keywords := gjson.ParseBytes(item.Keywords)

	item.AliasMap = json.RawMessage(util.Must(json.Marshal(keywords.Get("alias").Value())))
	item.PronMap = json.RawMessage(util.Must(json.Marshal(keywords.Get("pron").Value())))
}
