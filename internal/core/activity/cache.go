package activity

import "github.com/penguin-statistics/backend-next/internal/pkg/cache"

var CacheActivities *cache.Singular[[]*Model]

func InitCache() {
	CacheActivities = cache.NewSingular[[]*Model]("activities")
}
