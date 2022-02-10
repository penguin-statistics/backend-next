package cache

import (
	"errors"
	"time"
)

var ErrNoKey = errors.New("no such key")

type Cache interface {
	Get(key string, dest interface{}) error
	Set(key string, value interface{}, expire time.Duration) error
	Delete(key string)
}
