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
SELECT competition_rank, ordinal_rank, player_id, display_name, team_name,
       COALESCE(position_code, ''), 0::bigint AS appearances,
       0::bigint AS minutes, points_milli
FROM (
    SELECT rank() OVER (ORDER BY scores.points_milli DESC) AS competition_rank,
           row_number() OVER (ORDER BY scores.points_milli DESC, scores.player_id DESC) AS ordinal_rank,
           scores.player_id::text AS player_id, players.display_name,
           COALESCE(current_team.name, '') AS team_name, scores.position_code, scores.points_milli
    FROM player_edition_score AS scores
    JOIN player AS players ON players.id = scores.player_id
    LEFT JOIN LATERAL (
        SELECT teams.name
        FROM player_edition_team AS membership
        JOIN team AS teams ON teams.id = membership.team_id
        WHERE membership.edition_id = scores.edition_id
          AND membership.player_id = scores.player_id
        ORDER BY membership.last_seen_at DESC, membership.team_id
        LIMIT 1
    ) AS current_team ON true
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
			&entry.TeamName,
			&entry.PositionCode,
			&entry.Appearances,
			&entry.Minutes,
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

// listMatchRatings ranks provider-published ratings from finished matches. Each
// match is weighted by minutes and the result is shrunk toward the edition's
// minutes-weighted mean with a 450-minute prior. The prior prevents one short,
// exceptional appearance from outranking sustained performance; its influence
// falls automatically as the player accumulates minutes.
func (r *PostgresRepository) listMatchRatings(ctx context.Context, query Query) ([]Entry, error) {
	const statement = `
WITH rated_appearances AS (
    SELECT stats.player_id,
           stats.rating,
           stats.minutes::bigint AS minutes
    FROM player_match_stat AS stats
    JOIN football_match AS matches ON matches.id = stats.match_id
    WHERE matches.edition_id = $1::uuid
      AND matches.status = 'finished'
      AND stats.rating IS NOT NULL
      AND stats.minutes > 0
),
edition_baseline AS (
    SELECT sum(rating * minutes) / NULLIF(sum(minutes), 0) AS mean_rating
    FROM rated_appearances
),
player_ratings AS (
    SELECT player_id,
           count(*)::bigint AS appearances,
           sum(minutes)::bigint AS minutes,
           sum(rating * minutes) AS weighted_rating_sum
    FROM rated_appearances
    GROUP BY player_id
),
rating_scores AS (
    SELECT ratings.player_id,
           players.display_name,
           COALESCE(profiles.primary_position, '') AS position_code,
           COALESCE(current_team.name, '') AS team_name,
           ratings.appearances,
           ratings.minutes,
           round(
               (ratings.weighted_rating_sum + baseline.mean_rating * $3::numeric)
               / (ratings.minutes + $3::numeric) * 1000
           )::bigint AS points_milli
    FROM player_ratings AS ratings
    CROSS JOIN edition_baseline AS baseline
    JOIN player AS players ON players.id = ratings.player_id
    LEFT JOIN player_edition_profile AS profiles
      ON profiles.edition_id = $1::uuid
     AND profiles.player_id = ratings.player_id
    LEFT JOIN LATERAL (
        SELECT teams.name
        FROM player_edition_team AS membership
        JOIN team AS teams ON teams.id = membership.team_id
        WHERE membership.edition_id = $1::uuid
          AND membership.player_id = ratings.player_id
        ORDER BY membership.last_seen_at DESC, membership.team_id
        LIMIT 1
    ) AS current_team ON true
    WHERE ($2::text = '' OR profiles.primary_position = $2::text)
)
SELECT competition_rank, ordinal_rank, player_id, display_name, team_name,
       position_code, appearances, minutes, points_milli
FROM (
    SELECT rank() OVER (ORDER BY points_milli DESC) AS competition_rank,
           row_number() OVER (ORDER BY points_milli DESC, player_id DESC) AS ordinal_rank,
           player_id::text AS player_id, display_name, team_name, position_code,
           appearances, minutes, points_milli
    FROM rating_scores
) AS ranked
ORDER BY ordinal_rank
LIMIT $4`

	rows, err := r.pool.Query(
		ctx, statement, query.EditionID, query.Position, ratingPriorMinutes, query.Limit,
	)
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
			&entry.TeamName,
			&entry.PositionCode,
			&entry.Appearances,
			&entry.Minutes,
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
SELECT players.id::text, players.display_name, COALESCE(current_team.name, ''),
       COALESCE(scores.position_code, '')
FROM player AS players
JOIN player_edition_score AS scores ON scores.player_id = players.id
LEFT JOIN LATERAL (
    SELECT teams.name
    FROM player_edition_team AS membership
    JOIN team AS teams ON teams.id = membership.team_id
    WHERE membership.edition_id = scores.edition_id
      AND membership.player_id = scores.player_id
    ORDER BY membership.last_seen_at DESC, membership.team_id
    LIMIT 1
) AS current_team ON true
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
		if err := rows.Scan(&player.ID, &player.DisplayName, &player.TeamName, &player.PositionCode); err != nil {
			return nil, fmt.Errorf("scan leaderboard player: %w", err)
		}
		players[player.ID] = player
	}
	return players, rows.Err()
}
