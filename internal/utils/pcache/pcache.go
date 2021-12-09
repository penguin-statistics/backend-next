package pcache

import "sync"

type Cache struct {
	*sync.Map
}

func New() *Cache {
	return &Cache{
		&sync.Map{},
	}
}
