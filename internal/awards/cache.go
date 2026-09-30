package awards

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxCacheKeys bounds how many distinct candidate queries a single process
	// keeps cached. Award selections are requested per edition and per filtered
	// category, so the key space is small and fixed in practice.
	maxCacheKeys = 32

	// fillTimeout bounds one recomputation. A fill is shared by every waiting
	// reader, so a client that disconnects must not cancel work the others are
	// blocked on, and a wedged database must not pin readers forever.
	fillTimeout = 30 * time.Second
)

// Loader reads candidates for a set of award codes. *Repository implements it.
type Loader interface {
	TopCandidates(context.Context, string, []string, int) (map[string][]Candidate, error)
}

// Service reads season-award candidates through a short-lived in-process cache.
type Service struct {
	loader Loader
	cache  *candidateCache
}

// NewCachedService wraps a loader with an in-process cache. A non-positive ttl
// disables caching and every read delegates to the loader.
func NewCachedService(loader Loader, ttl time.Duration) *Service {
	return &Service{loader: loader, cache: newCandidateCache(ttl)}
}

// TopCandidates returns the leading candidates for each requested award code.
func (s *Service) TopCandidates(ctx context.Context, editionID string, awardCodes []string, limit int) (map[string][]Candidate, error) {
	if s.cache == nil {
		return s.loader.TopCandidates(ctx, editionID, awardCodes, limit)
	}
	return s.cache.get(ctx, candidateKey(editionID, awardCodes, limit), func(ctx context.Context) (map[string][]Candidate, error) {
		return s.loader.TopCandidates(ctx, editionID, awardCodes, limit)
	})
}

// candidateEntry is one cached candidate set with its expiry.
type candidateEntry struct {
	candidates map[string][]Candidate
	expiresAt  time.Time
}

// candidateCache holds candidate sets in the web process for a short window.
//
// Provisional candidates are recomputed from finished match facts, so the only
// change between two reads is a newly ingested match. A short time-to-live
// therefore serves every poller and every open tab from one query. It is a
// read-through cache: on a miss it delegates to the authoritative loader and
// never caches an error.
type candidateCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]candidateEntry

	// loading collapses concurrent misses for the same key onto a single
	// underlying load, so a burst of pollers cannot stampede PostgreSQL.
	loading sync.Map // string -> *fillCall
}

// fillCall is one in-flight fill shared by concurrent readers.
type fillCall struct {
	done       chan struct{}
	candidates map[string][]Candidate
	err        error
}

func newCandidateCache(ttl time.Duration) *candidateCache {
	if ttl <= 0 {
		return nil
	}
	return &candidateCache{ttl: ttl, entries: make(map[string]candidateEntry)}
}

// get returns cached candidates or loads and stores one.
func (c *candidateCache) get(ctx context.Context, key string, load func(context.Context) (map[string][]Candidate, error)) (map[string][]Candidate, error) {
	if c == nil {
		return load(ctx)
	}
	if candidates, ok := c.lookup(key); ok {
		return candidates, nil
	}

	// One LoadOrStore decides the leader. Storing a placeholder first and
	// replacing it afterwards would let a reader latch onto a call whose done
	// channel is never closed, which blocks that reader until its request is
	// cancelled.
	stored, loaded := c.loading.LoadOrStore(key, &fillCall{done: make(chan struct{})})
	call := stored.(*fillCall)
	if loaded {
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return call.candidates, call.err
	}

	// A panic inside load must release the waiters, and they must not read the
	// leader's half-built result as a successful empty answer.
	defer func() {
		recovered := recover()
		if recovered != nil {
			call.err = fmt.Errorf("load season award candidates: %v", recovered)
		}
		c.loading.Delete(key)
		close(call.done)
		if recovered != nil {
			panic(recovered)
		}
	}()

	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fillTimeout)
	defer cancel()

	candidates, err := load(loadCtx)
	if err != nil {
		call.err = err
		return nil, err
	}
	c.store(key, candidates)
	call.candidates = candidates
	return candidates, nil
}

// lookup returns a copy of a live entry so callers cannot mutate the cache.
func (c *candidateCache) lookup(key string) (map[string][]Candidate, bool) {
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
	return cloneCandidates(entry.candidates), true
}

func (c *candidateCache) store(key string, candidates map[string][]Candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxCacheKeys {
		c.evictExpired()
	}
	if len(c.entries) >= maxCacheKeys {
		clear(c.entries)
	}
	c.entries[key] = candidateEntry{
		candidates: cloneCandidates(candidates),
		expiresAt:  time.Now().Add(c.ttl),
	}
}

// evictExpired drops stale entries. The caller holds c.mu.
func (c *candidateCache) evictExpired() {
	now := time.Now()
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

func cloneCandidates(candidates map[string][]Candidate) map[string][]Candidate {
	if candidates == nil {
		return nil
	}
	clone := make(map[string][]Candidate, len(candidates))
	for code, list := range candidates {
		clone[code] = append([]Candidate(nil), list...)
	}
	return clone
}

// candidateKey identifies a cached candidate query. Award codes are sorted so
// that callers asking for the same set in a different order share one entry.
func candidateKey(editionID string, awardCodes []string, limit int) string {
	codes := slices.Clone(awardCodes)
	slices.Sort(codes)
	return editionID + "\x00" + strings.Join(codes, "\x00") + "\x00" + strconv.Itoa(limit)
}
