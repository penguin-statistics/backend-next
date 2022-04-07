package account

import "github.com/penguin-statistics/backend-next/internal/pkg/cache"

var (
	CacheByID        *cache.Set[Model]
	CacheByPenguinID *cache.Set[Model]
)

func InitCache() {
	CacheByID = cache.NewSet[Model]("account#accountId")
	CacheByPenguinID = cache.NewSet[Model]("account#penguinId")
}
