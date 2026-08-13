package leaderboard

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresRepository reads current scoring projections.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository creates a PostgreSQL leaderboard repository.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

// List returns the canonical ranking, including competition and stable ordinal ranks.
func (r *PostgresRepository) List(ctx context.Context, query Query) ([]Entry, error) {
	if query.UsesMatchRatings() {
		return r.listMatchRatings(ctx, query)
	}

	const statement = `
SELECT competition_rank, ordinal_rank, player_id, display_name,
       COALESCE(position_code, ''), points_milli
FROM (
    SELECT rank() OVER (ORDER BY scores.points_milli DESC) AS competition_rank,
           row_number() OVER (ORDER BY scores.points_milli DESC, scores.player_id DESC) AS ordinal_rank,
           scores.player_id::text AS player_id, players.display_name, scores.position_code, scores.points_milli
    FROM player_edition_score AS scores
    JOIN player AS players ON players.id = scores.player_id
    WHERE scores.edition_id = $1::uuid
      AND scores.scoring_model_id = $2::uuid
      AND ($3::text = '' OR scores.position_code = $3::text)
) AS ranked
ORDER BY ordinal_rank
LIMIT $4`

	rows, err := r.pool.Query(ctx, statement, query.EditionID, query.ScoringModelID, query.Position, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("query leaderboard: %w", err)
	}
	defer rows.Close()

	entries := make([]Entry, 0, query.Limit)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(
			&entry.CompetitionRank,
			&entry.OrdinalRank,
			&entry.PlayerID,
			&entry.PlayerName,
			&entry.PositionCode,
			&entry.PointsMilli,
		); err != nil {
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate leaderboard: %w", err)
	}
	return entries, nil
}

// listMatchRatings ranks each player's average provider-published match rating
// across the selected edition. Ratings are converted to millipoints so the
// existing exact leaderboard representation and formatter can be reused.
func (r *PostgresRepository) listMatchRatings(ctx context.Context, query Query) ([]Entry, error) {
	const statement = `
WITH rating_scores AS (
    SELECT stats.player_id,
           players.display_name,
           COALESCE(profiles.primary_position, '') AS position_code,
           round(avg(stats.rating) * 1000)::bigint AS points_milli
    FROM player_match_stat AS stats
    JOIN football_match AS matches ON matches.id = stats.match_id
    JOIN player AS players ON players.id = stats.player_id
    LEFT JOIN player_edition_profile AS profiles
      ON profiles.edition_id = matches.edition_id
     AND profiles.player_id = stats.player_id
    WHERE matches.edition_id = $1::uuid
      AND stats.rating IS NOT NULL
      AND ($2::text = '' OR profiles.primary_position = $2::text)
    GROUP BY stats.player_id, players.display_name, profiles.primary_position
)
SELECT competition_rank, ordinal_rank, player_id, display_name, position_code, points_milli
FROM (
    SELECT rank() OVER (ORDER BY points_milli DESC) AS competition_rank,
           row_number() OVER (ORDER BY points_milli DESC, player_id DESC) AS ordinal_rank,
           player_id::text AS player_id, display_name, position_code, points_milli
    FROM rating_scores
) AS ranked
ORDER BY ordinal_rank
LIMIT $3`

	rows, err := r.pool.Query(ctx, statement, query.EditionID, query.Position, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("query player match rating leaderboard: %w", err)
	}
	defer rows.Close()

	entries := make([]Entry, 0, query.Limit)
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(
			&entry.CompetitionRank,
			&entry.OrdinalRank,
			&entry.PlayerID,
			&entry.PlayerName,
			&entry.PositionCode,
			&entry.PointsMilli,
		); err != nil {
			return nil, fmt.Errorf("scan player match rating leaderboard: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate player match rating leaderboard: %w", err)
	}
	return entries, nil
}

// FindPlayers resolves Redis UUID members to display data.
func (r *PostgresRepository) FindPlayers(ctx context.Context, query Query, ids []string) (map[string]Player, error) {
	if len(ids) == 0 {
		return map[string]Player{}, nil
	}
	const statement = `
SELECT players.id::text, players.display_name, COALESCE(scores.position_code, '')
FROM player AS players
JOIN player_edition_score AS scores ON scores.player_id = players.id
WHERE scores.edition_id = $1::uuid
  AND scores.scoring_model_id = $2::uuid
  AND players.id::text = ANY($3::text[])`
	rows, err := r.pool.Query(ctx, statement, query.EditionID, query.ScoringModelID, ids)
	if err != nil {
		return nil, fmt.Errorf("resolve leaderboard players: %w", err)
	}
	defer rows.Close()

	players := make(map[string]Player, len(ids))
	for rows.Next() {
		var player Player
		if err := rows.Scan(&player.ID, &player.DisplayName, &player.PositionCode); err != nil {
			return nil, fmt.Errorf("scan leaderboard player: %w", err)
		}
		players[player.ID] = player
	}
	return players, rows.Err()
}
