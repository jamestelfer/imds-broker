package imdsserver

import (
	"sync"
	"time"
)

// Cached is a generic TTL cache with lazy refresh. The cached value is
// refreshed by calling fetch when the TTL has expired. On fetch error, the
// stale value is returned if the cache has been populated; otherwise the zero
// value and the error are returned.
type Cached[T any] struct {
	mu      sync.Mutex
	val     T
	updated time.Time
	ttl     time.Duration
	fetch   func() (T, error)
}

// NewCached constructs a Cached[T] with the given TTL and fetch function.
func NewCached[T any](ttl time.Duration, fetch func() (T, error)) *Cached[T] {
	return &Cached[T]{ttl: ttl, fetch: fetch}
}

// Get returns the cached value if within TTL, otherwise calls fetch to refresh
// it. On fetch error, the stale value is returned if available.
func (c *Cached[T]) Get() (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.updated) < c.ttl {
		return c.val, nil
	}
	v, err := c.fetch()
	if err != nil {
		return c.val, err
	}
	c.val = v
	c.updated = time.Now()
	return v, nil
}
