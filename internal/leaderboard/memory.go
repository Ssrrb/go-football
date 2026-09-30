package leaderboard

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// defaultLimit bounds how many distinct queries a single process keeps cached.
const maxMemoryKeys = 128

// memoryEntry is one cached ranking with its expiry.
type memoryEntry struct {
	entries   []Entry
	expiresAt time.Time
}

// MemoryCache holds ranking results in the web process for a short window.
//
// The ranking is derived from finished match facts and only changes when a
// new match is ingested, so a short time-to-live serves many concurrent readers
// from a single query. It is a read-through cache: on a miss it delegates to the
// authoritative loader and never caches an error.
type MemoryCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]memoryEntry

	// loading collapses concurrent misses for the same query onto a single
	// underlying load so a burst of pollers cannot stampede PostgreSQL.
	loading sync.Map // string -> *loadCall
}

// loadCall is one in-flight fill shared by concurrent readers.
type loadCall struct {
	done    chan struct{}
	entries []Entry
	err     error
}

// NewMemoryCache creates an in-process ranking cache. A non-positive ttl
// disables caching and every read delegates to the loader.
func NewMemoryCache(ttl time.Duration) *MemoryCache {
	return &MemoryCache{ttl: ttl, entries: make(map[string]memoryEntry)}
}

// get returns a cached ranking or loads and stores one.
func (c *MemoryCache) get(ctx context.Context, query Query, load func(context.Context, Query) ([]Entry, error)) ([]Entry, error) {
	if c.ttl <= 0 {
		return load(ctx, query)
	}
	key := memoryKey(query)
	if entries, ok := c.lookup(key); ok {
		return entries, nil
	}

	// The load is detached from the caller's context because a fill serves
	// every waiting reader; a client that disconnects mid-request must not
	// discard work the others are already blocked on.
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.ttl)
	defer cancel()

	// One LoadOrStore decides the leader. Storing a placeholder first and
	// replacing it afterwards lets a reader latch onto a call whose done channel
	// is never closed, which blocks that reader until its request is cancelled.
	stored, loaded := c.loading.LoadOrStore(key, &loadCall{done: make(chan struct{})})
	call := stored.(*loadCall)
	if loaded {
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return call.entries, call.err
	}

	// A panic inside load must release the waiters, and they must not read the
	// leader's half-built result as a successful empty answer.
	defer func() {
		recovered := recover()
		if recovered != nil {
			call.err = fmt.Errorf("load leaderboard ranking: %v", recovered)
		}
		c.loading.Delete(key)
		close(call.done)
		if recovered != nil {
			panic(recovered)
		}
	}()

	entries, err := load(loadCtx, query)
	if err != nil {
		call.err = err
		return nil, err
	}
	c.store(key, entries)
	call.entries = entries
	return entries, nil
}

// lookup returns a copy of a live entry so callers cannot mutate the cache.
func (c *MemoryCache) lookup(key string) ([]Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !time.Now().Before(entry.expiresAt) {
		delete(c.entries, key)
		return nil, false
	}
	return append([]Entry(nil), entry.entries...), true
}

func (c *MemoryCache) store(key string, entries []Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxMemoryKeys {
		c.evictExpired()
	}
	if len(c.entries) >= maxMemoryKeys {
		clear(c.entries)
	}
	c.entries[key] = memoryEntry{
		entries:   append([]Entry(nil), entries...),
		expiresAt: time.Now().Add(c.ttl),
	}
}

// evictExpired drops stale entries. The caller holds c.mu.
func (c *MemoryCache) evictExpired() {
	now := time.Now()
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// memoryKey identifies a cached ranking. Limit is normalized before this is
// called, so equal queries always share a key.
func memoryKey(query Query) string {
	return query.EditionID + "\x00" + query.ScoringModelID + "\x00" + query.Position + "\x00" +
		string(rune('0'+min(query.Limit, 100)/10)) + string(rune('0'+min(query.Limit, 100)%10))
}
