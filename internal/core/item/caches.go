package item

import (
	"github.com/penguin-statistics/backend-next/internal/pkg/cache"
)

var (
	CachedItems           *cache.Singular[[]*Model]
	CachedItemByArkID     *cache.Set[Model]
	CachedItemsMapById    *cache.Singular[map[int]*Model]
	CachedItemsMapByArkID *cache.Singular[map[string]*Model]
)

func InitCache() {
	CachedItems = cache.NewSingular[[]*Model]("items")
	CachedItemByArkID = cache.NewSet[Model]("item#arkItemId")
	CachedItemsMapById = cache.NewSingular[map[int]*Model]("itemsMapById")
	CachedItemsMapByArkID = cache.NewSingular[map[string]*Model]("itemsMapByArkId")
}
