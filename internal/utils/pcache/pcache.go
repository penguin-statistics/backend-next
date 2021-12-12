package pcache

import (
	"time"

	"github.com/patrickmn/go-cache"
)

func New() *cache.Cache {
	return cache.New(cache.NoExpiration, time.Hour)
}
