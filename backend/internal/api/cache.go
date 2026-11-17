package api

import (
	"context"
	"sync"
	"time"
)

// ttlCache caches one value per key for ttl, with single-flight fetches
// (mirrors releaseArtifactsCache). Failed fetches are not cached.
type ttlCache[T any] struct {
	mu       sync.Mutex
	ttl      time.Duration
	entries  map[string]ttlCacheEntry[T]
	inflight map[string]chan struct{}
}

type ttlCacheEntry[T any] struct {
	value   T
	expires time.Time
	err     error
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, entries: map[string]ttlCacheEntry[T]{}, inflight: map[string]chan struct{}{}}
}

func (c *ttlCache[T]) Get(ctx context.Context, key string, fetch func(context.Context) (T, error)) (T, error) {
	var zero T
	now := time.Now()
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && now.Before(entry.expires) {
		c.mu.Unlock()
		return entry.value, entry.err
	}
	if wait, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
		c.mu.Lock()
		entry := c.entries[key]
		c.mu.Unlock()
		return entry.value, entry.err
	}
	wait := make(chan struct{})
	c.inflight[key] = wait
	c.mu.Unlock()

	value, err := fetch(ctx)
	c.mu.Lock()
	expires := time.Now()
	if err == nil {
		expires = expires.Add(c.ttl)
	}
	c.entries[key] = ttlCacheEntry[T]{value: value, expires: expires, err: err}
	delete(c.inflight, key)
	close(wait)
	c.mu.Unlock()
	return value, err
}
