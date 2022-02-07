package cache

import (
	"sync"

	"github.com/go-redis/redis/v8"

	"github.com/penguin-statistics/backend-next/internal/utils/cache"
)

var (
	ItemFromId    cache.Cache
	ItemFromArkId cache.Cache
)

var (
	StageFromId    cache.Cache
	StageFromArkId cache.Cache
)

var (
	ZoneFromId    cache.Cache
	ZoneFromArkId cache.Cache
)

var once sync.Once

func Populate(client *redis.Client) {
	once.Do(func() {
		ItemFromId = cache.New(client, "item#id")
		ItemFromArkId = cache.New(client, "item#arkId")

		StageFromId = cache.New(client, "stage#id")
		StageFromArkId = cache.New(client, "stage#arkId")

		ZoneFromId = cache.New(client, "zone#id")
		ZoneFromArkId = cache.New(client, "zone#arkId")
	})
}
