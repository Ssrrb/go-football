package leaderboard

import (
	"context"
	"errors"
	"testing"
)

type repositoryStub struct {
	entries []Entry
	players map[string]Player
}

func (r repositoryStub) List(context.Context, Query) ([]Entry, error) { return r.entries, nil }
func (r repositoryStub) FindPlayers(context.Context, Query, []string) (map[string]Player, error) {
	return r.players, nil
}

type cacheStub struct {
	entries []CachedEntry
	err     error
	calls   int
}

func (c *cacheStub) Top(context.Context, Query) ([]CachedEntry, error) {
	c.calls++
	return c.entries, c.err
}

func TestServiceEnrichesCachedRanking(t *testing.T) {
	service := NewService(repositoryStub{players: map[string]Player{
		"player-1": {ID: "player-1", DisplayName: "Ada Striker", TeamName: "Club Olimpia", PositionCode: "FWD"},
	}}, &cacheStub{entries: []CachedEntry{{PlayerID: "player-1", CompetitionRank: 1, OrdinalRank: 1, PointsMilli: 1250}}})

	entries, err := service.List(context.Background(), Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PlayerName != "Ada Striker" || entries[0].TeamName != "Club Olimpia" || entries[0].Points() != "1.250" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestServiceFallsBackWhenCacheFails(t *testing.T) {
	fallback := []Entry{{PlayerID: "player-2"}}
	service := NewService(repositoryStub{entries: fallback}, &cacheStub{err: errors.New("offline")})
	entries, err := service.List(context.Background(), Query{})
	if err != nil || len(entries) != 1 || entries[0].PlayerID != "player-2" {
		t.Fatalf("expected PostgreSQL fallback, got %#v, %v", entries, err)
	}
}

func TestServiceBypassesCacheForDefaultBasePerformance(t *testing.T) {
	fallback := []Entry{{PlayerID: "base-player", PointsMilli: 7350}}
	cache := &cacheStub{entries: []CachedEntry{{PlayerID: "stale-player"}}}
	service := NewService(repositoryStub{entries: fallback}, cache)

	entries, err := service.List(context.Background(), Query{ScoringModelID: BasePerformanceModelID})
	if err != nil || len(entries) != 1 || entries[0].PlayerID != "base-player" {
		t.Fatalf("expected PostgreSQL base performance, got %#v, %v", entries, err)
	}
	if cache.calls != 0 {
		t.Fatalf("expected base-performance query to bypass Redis, got %d calls", cache.calls)
	}
}

func TestServiceRejectsUnknownPosition(t *testing.T) {
	service := NewService(repositoryStub{}, nil)
	if _, err := service.List(context.Background(), Query{Position: "CB"}); err == nil {
		t.Fatal("expected invalid position error")
	}
}

func TestPointsFormatsMillipointsExactly(t *testing.T) {
	for value, expected := range map[int64]string{1250: "1.250", -5: "-0.005", 9_000_000_000_000_000: "9000000000000.000"} {
		if actual := (Entry{PointsMilli: value}).Points(); actual != expected {
			t.Errorf("Points() for %d = %q, want %q", value, actual, expected)
		}
	}
}

func TestSampleConfidenceGrowsWithMinutesReliability(t *testing.T) {
	for minutes, expected := range map[int64]string{
		0:    "0%",
		90:   "9%",
		900:  "50%",
		1800: "67%",
	} {
		entry := Entry{Minutes: minutes}
		if actual := entry.SampleConfidence(); actual != expected {
			t.Errorf("SampleConfidence() for %d minutes = %q, want %q", minutes, actual, expected)
		}
	}
}
