package leaderboard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingRepository struct {
	calls   atomic.Int64
	entries []Entry
	err     error
	delay   time.Duration
}

func (r *countingRepository) List(ctx context.Context, _ Query) ([]Entry, error) {
	r.calls.Add(1)
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.entries, r.err
}

func (r *countingRepository) FindPlayers(context.Context, Query, []string) (map[string]Player, error) {
	return nil, nil
}

func TestMemoryCacheServesRepeatsWithinTTL(t *testing.T) {
	repository := &countingRepository{entries: []Entry{{PlayerID: "p1", PointsMilli: 1000}}}
	service := NewCachedService(repository, nil, time.Minute)

	for range 5 {
		entries, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10})
		if err != nil || len(entries) != 1 || entries[0].PlayerID != "p1" {
			t.Fatalf("unexpected entries: %#v, %v", entries, err)
		}
	}
	if got := repository.calls.Load(); got != 1 {
		t.Fatalf("expected 1 load within TTL, got %d", got)
	}
}

func TestMemoryCacheSeparatesPositions(t *testing.T) {
	repository := &countingRepository{entries: []Entry{{PlayerID: "p1"}}}
	service := NewCachedService(repository, nil, time.Minute)

	for _, position := range []string{"", "GK", "DEF", "GK"} {
		if _, err := service.List(context.Background(), Query{EditionID: "e", Position: position, Limit: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if got := repository.calls.Load(); got != 3 {
		t.Fatalf("expected 3 loads for 3 distinct queries, got %d", got)
	}
}

func TestMemoryCacheReloadsAfterTTL(t *testing.T) {
	repository := &countingRepository{entries: []Entry{{PlayerID: "p1"}}}
	service := NewCachedService(repository, nil, 20*time.Millisecond)

	if _, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if got := repository.calls.Load(); got != 2 {
		t.Fatalf("expected reload after TTL, got %d loads", got)
	}
}

func TestMemoryCacheDoesNotCacheErrors(t *testing.T) {
	repository := &countingRepository{err: errors.New("offline")}
	service := NewCachedService(repository, nil, time.Minute)

	for range 3 {
		if _, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10}); err == nil {
			t.Fatal("expected error")
		}
	}
	if got := repository.calls.Load(); got != 3 {
		t.Fatalf("expected an error to be retried, got %d loads", got)
	}
}

func TestMemoryCacheCollapsesConcurrentMisses(t *testing.T) {
	repository := &countingRepository{entries: []Entry{{PlayerID: "p1"}}, delay: 50 * time.Millisecond}
	service := NewCachedService(repository, nil, time.Minute)

	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if got := repository.calls.Load(); got != 1 {
		t.Fatalf("expected concurrent readers to share one load, got %d", got)
	}
}

func TestMemoryCacheDisabledByZeroTTL(t *testing.T) {
	repository := &countingRepository{entries: []Entry{{PlayerID: "p1"}}}
	service := NewCachedService(repository, nil, 0)

	for range 3 {
		if _, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if got := repository.calls.Load(); got != 3 {
		t.Fatalf("expected no caching with zero TTL, got %d loads", got)
	}
}

func TestMemoryCacheReturnsIndependentSlices(t *testing.T) {
	repository := &countingRepository{entries: []Entry{{PlayerID: "p1"}}}
	service := NewCachedService(repository, nil, time.Minute)

	first, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	first[0].PlayerID = "mutated"

	second, err := service.List(context.Background(), Query{EditionID: "e", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].PlayerID != "p1" {
		t.Fatal("caller mutated the cached entry")
	}
}

func TestMemoryCacheSurvivesLoaderPanic(t *testing.T) {
	// A panicking load must not leave the key stuck in the in-flight map,
	// which would block every later reader for the process lifetime.
	cache := NewMemoryCache(time.Minute)
	query := Query{EditionID: "e", Limit: 10}

	func() {
		defer func() { _ = recover() }()
		_, _ = cache.get(context.Background(), query, func(context.Context, Query) ([]Entry, error) {
			panic("boom")
		})
	}()

	if _, ok := cache.lookup(memoryKey(query)); ok {
		t.Fatal("panicking load should not populate the cache")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		entries, err := cache.get(context.Background(), query, func(context.Context, Query) ([]Entry, error) {
			return []Entry{{PlayerID: "recovered"}}, nil
		})
		if err != nil || len(entries) != 1 || entries[0].PlayerID != "recovered" {
			t.Errorf("did not recover after panic: %#v, %v", entries, err)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader blocked forever after a panicking load")
	}
}

func TestMemoryCacheWaiterSurvivesPanickingLeader(t *testing.T) {
	// The leader panics before storing its result. The waiter must be released
	// rather than hang or read a half-built entry.
	cache := NewMemoryCache(time.Minute)
	query := Query{EditionID: "e", Limit: 10}
	release := make(chan struct{})
	leaderStarted := make(chan struct{})

	go func() {
		defer func() { _ = recover() }()
		_, _ = cache.get(context.Background(), query, func(context.Context, Query) ([]Entry, error) {
			close(leaderStarted)
			<-release
			panic("boom")
		})
	}()
	<-leaderStarted

	waiterRanLoad := make(chan struct{})
	waiterDone := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), query, func(context.Context, Query) ([]Entry, error) {
			close(waiterRanLoad)
			return []Entry{{PlayerID: "late"}}, nil
		})
		waiterDone <- err
	}()

	// Give the waiter time to latch onto the in-flight call before the leader
	// unwinds. If it loses that race it becomes the leader itself, and there is
	// no error to propagate.
	time.Sleep(50 * time.Millisecond)
	close(release)
	select {
	case err := <-waiterDone:
		select {
		case <-waiterRanLoad:
			return
		default:
		}
		if err == nil {
			t.Fatal("expected the waiter to be told the fill failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter blocked forever after leader panicked")
	}
}

func TestMemoryCacheBoundsStoredKeys(t *testing.T) {
	cache := NewMemoryCache(time.Minute)
	for index := range maxMemoryKeys * 2 {
		_, _ = cache.get(
			context.Background(),
			Query{EditionID: "e", Position: "P" + string(rune('A'+index%26)) + string(rune('a'+index/26)), Limit: 10},
			func(context.Context, Query) ([]Entry, error) { return []Entry{{PlayerID: "p"}}, nil },
		)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) > maxMemoryKeys {
		t.Fatalf("cache grew past the bound: %d keys", len(cache.entries))
	}
}
