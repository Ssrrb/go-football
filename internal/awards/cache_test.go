package awards

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingLoader struct {
	calls      atomic.Int64
	candidates map[string][]Candidate
	err        error
	delay      time.Duration
}

func (l *countingLoader) TopCandidates(ctx context.Context, _ string, codes []string, _ int) (map[string][]Candidate, error) {
	l.calls.Add(1)
	if l.delay > 0 {
		select {
		case <-time.After(l.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if l.err != nil {
		return nil, l.err
	}
	return l.candidates, nil
}

func sampleCandidates() map[string][]Candidate {
	return map[string][]Candidate{
		PlayerOfSeason: {{AwardCode: PlayerOfSeason, Rank: 1, PlayerName: "Ada", ScoreMilli: 95_420}},
	}
}

func TestCandidateCacheServesRepeatsWithinTTL(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates()}
	service := NewCachedService(loader, time.Minute)

	for range 5 {
		candidates, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidates[PlayerOfSeason]) != 1 || candidates[PlayerOfSeason][0].PlayerName != "Ada" {
			t.Fatalf("unexpected candidates: %#v", candidates)
		}
	}
	if got := loader.calls.Load(); got != 1 {
		t.Fatalf("expected 1 load within the TTL, got %d", got)
	}
}

func TestCandidateCacheSeparatesEditionsAndLimits(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates()}
	service := NewCachedService(loader, time.Minute)

	requests := []struct {
		edition string
		limit   int
	}{
		{"a", 3}, {"b", 3}, {"a", 5}, {"a", 3},
	}
	for _, item := range requests {
		if _, err := service.TopCandidates(context.Background(), item.edition, []string{PlayerOfSeason}, item.limit); err != nil {
			t.Fatal(err)
		}
	}
	if got := loader.calls.Load(); got != 3 {
		t.Fatalf("expected 3 loads for 3 distinct queries, got %d", got)
	}
}

func TestCandidateCacheIgnoresAwardCodeOrder(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates()}
	service := NewCachedService(loader, time.Minute)

	first := []string{PlayerOfSeason, DefenderOfSeason}
	second := []string{DefenderOfSeason, PlayerOfSeason}
	if _, err := service.TopCandidates(context.Background(), "edition", first, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TopCandidates(context.Background(), "edition", second, 3); err != nil {
		t.Fatal(err)
	}
	if got := loader.calls.Load(); got != 1 {
		t.Fatalf("expected reordered codes to hit the same entry, got %d loads", got)
	}
	// The key must not reorder the caller's slice.
	if first[0] != PlayerOfSeason {
		t.Fatal("candidateKey mutated the caller's award codes")
	}
}

func TestCandidateCacheReloadsAfterTTL(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates()}
	service := NewCachedService(loader, 20*time.Millisecond)

	if _, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3); err != nil {
		t.Fatal(err)
	}
	if got := loader.calls.Load(); got != 2 {
		t.Fatalf("expected a reload after the TTL, got %d loads", got)
	}
}

func TestCandidateCacheDoesNotCacheErrors(t *testing.T) {
	loader := &countingLoader{err: errors.New("offline")}
	service := NewCachedService(loader, time.Minute)

	for range 3 {
		if _, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3); err == nil {
			t.Fatal("expected an error")
		}
	}
	if got := loader.calls.Load(); got != 3 {
		t.Fatalf("expected an error to be retried, got %d loads", got)
	}
}

func TestCandidateCacheCollapsesConcurrentMisses(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates(), delay: 50 * time.Millisecond}
	service := NewCachedService(loader, time.Minute)

	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if got := loader.calls.Load(); got != 1 {
		t.Fatalf("expected concurrent readers to share one load, got %d", got)
	}
}

func TestCandidateCacheDisabledByZeroTTL(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates()}
	service := NewCachedService(loader, 0)

	for range 3 {
		if _, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3); err != nil {
			t.Fatal(err)
		}
	}
	if got := loader.calls.Load(); got != 3 {
		t.Fatalf("expected no caching with a zero TTL, got %d loads", got)
	}
}

func TestCandidateCacheReturnsIndependentCopies(t *testing.T) {
	loader := &countingLoader{candidates: sampleCandidates()}
	service := NewCachedService(loader, time.Minute)

	first, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3)
	if err != nil {
		t.Fatal(err)
	}
	first[PlayerOfSeason][0].PlayerName = "mutated"
	delete(first, PlayerOfSeason)

	second, err := service.TopCandidates(context.Background(), "edition", []string{PlayerOfSeason}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(second[PlayerOfSeason]) != 1 || second[PlayerOfSeason][0].PlayerName != "Ada" {
		t.Fatal("caller mutated the cached candidates")
	}
}

func TestCandidateCacheSurvivesLoaderPanic(t *testing.T) {
	// A panicking load must not leave the key stuck in the in-flight map,
	// which would block every later reader for the process lifetime.
	cache := newCandidateCache(time.Minute)
	key := candidateKey("edition", []string{PlayerOfSeason}, 3)

	func() {
		defer func() { _ = recover() }()
		_, _ = cache.get(context.Background(), key, func(context.Context) (map[string][]Candidate, error) {
			panic("boom")
		})
	}()

	if _, ok := cache.lookup(key); ok {
		t.Fatal("a panicking load must not populate the cache")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		candidates, err := cache.get(context.Background(), key, func(context.Context) (map[string][]Candidate, error) {
			return sampleCandidates(), nil
		})
		if err != nil || len(candidates[PlayerOfSeason]) != 1 {
			t.Errorf("did not recover after a panic: %#v, %v", candidates, err)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader blocked forever after a panicking load")
	}
}

func TestCandidateCacheWaiterSurvivesPanickingLeader(t *testing.T) {
	// The leader panics before storing its result. The waiter must be released
	// with an error rather than hang or read a half-built entry as a successful
	// empty answer.
	cache := newCandidateCache(time.Minute)
	key := candidateKey("edition", []string{PlayerOfSeason}, 3)
	release := make(chan struct{})
	leaderStarted := make(chan struct{})

	go func() {
		defer func() { _ = recover() }()
		_, _ = cache.get(context.Background(), key, func(context.Context) (map[string][]Candidate, error) {
			close(leaderStarted)
			<-release
			panic("boom")
		})
	}()
	<-leaderStarted

	waiterRanLoad := make(chan struct{})
	waiterDone := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), key, func(context.Context) (map[string][]Candidate, error) {
			close(waiterRanLoad)
			return sampleCandidates(), nil
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
		t.Fatal("waiter blocked forever after the leader panicked")
	}
}

func TestCandidateCacheBoundsStoredKeys(t *testing.T) {
	cache := newCandidateCache(time.Minute)
	for index := range maxCacheKeys * 2 {
		edition := "edition-" + string(rune('a'+index%26)) + string(rune('a'+index/26))
		if _, err := cache.get(context.Background(), candidateKey(edition, []string{PlayerOfSeason}, 3),
			func(context.Context) (map[string][]Candidate, error) { return sampleCandidates(), nil },
		); err != nil {
			t.Fatal(err)
		}
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) > maxCacheKeys {
		t.Fatalf("cache grew past the bound: %d keys", len(cache.entries))
	}
}
