// Package leaderboard provides leaderboard queries independent of HTTP.
package leaderboard

import (
	"context"
	"fmt"

	"github.com/Ssrrb/go-football/internal/performance"
)

const (
	defaultLimit = 50

	// BasePerformanceModelID selects the provider-independent positional
	// performance algorithm instead of a pre-published scoring model.
	BasePerformanceModelID = "default"
)

// Entry is one ranked football player.
type Entry struct {
	CompetitionRank int64
	OrdinalRank     int64
	PlayerID        string
	PlayerName      string
	TeamName        string
	PositionCode    string
	Appearances     int64
	Minutes         int64
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

// SampleConfidence reports the base algorithm's minutes reliability. At 900
// minutes the player's normalized performance contributes 50% of its eventual
// full-reliability value.
func (e Entry) SampleConfidence() string {
	if e.Minutes <= 0 {
		return "0%"
	}
	confidence := 100 * float64(e.Minutes) / (float64(e.Minutes) + performance.ReliabilityPriorMinutes)
	return fmt.Sprintf("%.0f%%", confidence)
}

// Query identifies one leaderboard projection.
type Query struct {
	EditionID      string
	ScoringModelID string
	Position       string
	Limit          int
}

// UsesBasePerformance reports whether the query selects the built-in,
// provider-independent positional performance algorithm.
func (q Query) UsesBasePerformance() bool {
	return q.ScoringModelID == BasePerformanceModelID
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
	TeamName     string
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
	// The default leaderboard is computed directly from normalized match
	// statistics and does not have a Redis scoring-model projection.
	if s.cache != nil && !query.UsesBasePerformance() {
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
			TeamName:        player.TeamName,
			PositionCode:    player.PositionCode,
			PointsMilli:     item.PointsMilli,
		})
	}
	return entries
}
