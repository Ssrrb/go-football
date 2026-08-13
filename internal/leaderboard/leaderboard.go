// Package leaderboard provides leaderboard queries independent of HTTP.
package leaderboard

import (
	"context"
	"fmt"
)

const defaultLimit = 50

// Entry is one ranked football player.
type Entry struct {
	CompetitionRank int64
	OrdinalRank     int64
	PlayerID        string
	PlayerName      string
	PositionCode    string
	PointsMilli     int64
}

// Points returns the public decimal representation of millipoints.
func (e Entry) Points() string {
	value := e.PointsMilli
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%03d", sign, value/1000, value%1000)
}

// Query identifies one leaderboard projection.
type Query struct {
	EditionID      string
	ScoringModelID string
	Position       string
	Limit          int
}

// Repository reads the authoritative PostgreSQL projection.
type Repository interface {
	List(context.Context, Query) ([]Entry, error)
	FindPlayers(context.Context, Query, []string) (map[string]Player, error)
}

// Cache reads a Redis sorted-set projection.
type Cache interface {
	Top(context.Context, Query) ([]CachedEntry, error)
}

// Player contains display data needed to enrich Redis members.
type Player struct {
	ID           string
	DisplayName  string
	PositionCode string
}

// CachedEntry is a ranked Redis sorted-set member.
type CachedEntry struct {
	PlayerID        string
	CompetitionRank int64
	OrdinalRank     int64
	PointsMilli     int64
}

// Service reads Redis first and safely falls back to PostgreSQL.
type Service struct {
	repository Repository
	cache      Cache
}

// NewService creates a leaderboard service.
func NewService(repository Repository, cache Cache) *Service {
	return &Service{repository: repository, cache: cache}
}

// List returns ranked entries for a validated position filter.
func (s *Service) List(ctx context.Context, query Query) ([]Entry, error) {
	if !validPosition(query.Position) {
		return nil, fmt.Errorf("unsupported position %q", query.Position)
	}
	if query.Limit <= 0 || query.Limit > 100 {
		query.Limit = defaultLimit
	}
	if s.cache != nil {
		cached, err := s.cache.Top(ctx, query)
		if err == nil && len(cached) > 0 {
			players, resolveErr := s.repository.FindPlayers(ctx, query, playerIDs(cached))
			if resolveErr == nil && len(players) == len(cached) {
				return enrich(cached, players), nil
			}
		}
	}
	return s.repository.List(ctx, query)
}

func validPosition(position string) bool {
	switch position {
	case "", "GK", "DEF", "MID", "FWD":
		return true
	default:
		return false
	}
}

func playerIDs(entries []CachedEntry) []string {
	ids := make([]string, len(entries))
	for index, entry := range entries {
		ids[index] = entry.PlayerID
	}
	return ids
}

func enrich(cached []CachedEntry, players map[string]Player) []Entry {
	entries := make([]Entry, 0, len(cached))
	for _, item := range cached {
		player := players[item.PlayerID]
		entries = append(entries, Entry{
			CompetitionRank: item.CompetitionRank,
			OrdinalRank:     item.OrdinalRank,
			PlayerID:        item.PlayerID,
			PlayerName:      player.DisplayName,
			PositionCode:    player.PositionCode,
			PointsMilli:     item.PointsMilli,
		})
	}
	return entries
}
