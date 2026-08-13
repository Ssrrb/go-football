package leaderboard

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisCache reads leaderboard sorted sets populated by the ETL/outbox worker.
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache creates a Redis leaderboard cache.
func NewRedisCache(client *redis.Client) *RedisCache {
	return &RedisCache{client: client}
}

// Top returns highest scoring members and computes competition ranks for ties.
func (c *RedisCache) Top(ctx context.Context, query Query) ([]CachedEntry, error) {
	values, err := c.client.ZRevRangeWithScores(ctx, redisKey(query), 0, int64(query.Limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("read Redis leaderboard: %w", err)
	}
	entries := make([]CachedEntry, 0, len(values))
	var previous float64
	var competitionRank int64
	for index, value := range values {
		if index == 0 || value.Score != previous {
			competitionRank = int64(index + 1)
		}
		member, ok := value.Member.(string)
		if !ok {
			return nil, fmt.Errorf("Redis leaderboard member was not a string")
		}
		entries = append(entries, CachedEntry{
			PlayerID:        member,
			CompetitionRank: competitionRank,
			OrdinalRank:     int64(index + 1),
			PointsMilli:     int64(value.Score),
		})
		previous = value.Score
	}
	return entries, nil
}

func redisKey(query Query) string {
	base := fmt.Sprintf("lb:v1:edition:%s:model:%s", query.EditionID, query.ScoringModelID)
	if query.Position == "" {
		return base + ":all"
	}
	return base + ":pos:" + query.Position
}
