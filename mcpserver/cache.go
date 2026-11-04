package mcpserver

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// maxFetchTime bounds a shared fetch, which runs detached from the callers'
// contexts. The default HTTP client has a shorter timeout; this guards
// against a caller-supplied client with none.
const maxFetchTime = 30 * time.Second

// cache memoises the result of fetch for ttl. Errors are not cached.
//
// Concurrent callers share a single in-flight fetch. Each caller waits only
// as long as its own context allows, and the fetch itself is detached from
// any one caller, so a caller giving up doesn't fail the others or waste the
// result. Cached values are shared between callers, so they must be treated
// as read-only.
type cache[T any] struct {
	ttl   time.Duration
	now   func() time.Time
	fetch func(context.Context) (T, error)
	group singleflight.Group

	mu        sync.Mutex
	value     T
	fetchedAt time.Time
	valid     bool
}

func newCache[T any](ttl time.Duration, now func() time.Time, fetch func(context.Context) (T, error)) *cache[T] {
	return &cache[T]{ttl: ttl, now: now, fetch: fetch}
}

func (c *cache[T]) get(ctx context.Context) (T, error) {
	if c.ttl <= 0 {
		return c.fetch(ctx)
	}

	if v, ok := c.cached(); ok {
		return v, nil
	}

	ch := c.group.DoChan("", func() (any, error) {
		// Another fetch may have completed since the check above.
		if v, ok := c.cached(); ok {
			return v, nil
		}

		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxFetchTime)
		defer cancel()

		v, err := c.fetch(fetchCtx)
		if err != nil {
			return nil, err
		}

		c.mu.Lock()
		c.value, c.fetchedAt, c.valid = v, c.now(), true
		c.mu.Unlock()

		return v, nil
	})

	var zero T

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return zero, r.Err
		}

		value, ok := r.Val.(T)
		if !ok {
			return zero, fmt.Errorf("githubstatus: cache returned %T, want cached value", r.Val)
		}

		return value, nil
	}
}

func (c *cache[T]) cached() (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.valid && c.now().Sub(c.fetchedAt) < c.ttl {
		return c.value, true
	}

	var zero T

	return zero, false
}
