package connection

import (
	"context"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"
)

const CacheConnectionKey = "CacheConnectionKey"

// Cache simple cache implemented using ristretto cache library
type Cache struct {
	cache *ristretto.Cache
}

func NewCache(config *ristretto.Config) *Cache {
	if config == nil {
		config = &ristretto.Config{
			NumCounters: 1e7,     // number of keys to track frequency of (10M).
			MaxCost:     1 << 30, // maximum cost of cache (1GB).
			BufferItems: 64,      // number of keys per Get buffer.
		}
	}
	cache, err := ristretto.NewCache(config)
	if err != nil {
		panic(err)
	}
	return &Cache{cache}
}

func (cache *Cache) Set(ctx context.Context, key string, value interface{}) bool {
	key = addConnectionKey(ctx, key)
	return cache.SetWithTTL(key, value, 1*time.Hour)
}

func (cache *Cache) SetWithTTL(key string, value interface{}, ttl time.Duration) bool {
	res := cache.cache.SetWithTTL(key, value, 1, ttl)
	// wait for value to pass through buffers
	time.Sleep(10 * time.Millisecond)
	return res
}

func (cache *Cache) Get(ctx context.Context, key string) (interface{}, bool) {
	key = addConnectionKey(ctx, key)
	return cache.cache.Get(key)
}

func addConnectionKey(ctx context.Context, key string) string {
	conKey := ctx.Value(CacheConnectionKey)
	if conKey == nil {
		return key
	}
	return fmt.Sprintf("%s-%s", key, conKey)
}

func (cache *Cache) Delete(key string) {
	cache.cache.Del(key)
}
