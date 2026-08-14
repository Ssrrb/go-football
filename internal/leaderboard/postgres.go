package leaderboard

import (
	"context"
	"fmt"

	"github.com/Ssrrb/go-football/internal/performance"
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
	if query.UsesBasePerformance() {
		return r.listBasePerformance(ctx, query)
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

// listBasePerformance computes the v3 base rating from finished-match facts.
// Inputs are robust-Z normalized inside each position, combined with positional
// weights, and reliability-adjusted. Provider-published ratings are not read.
func (r *PostgresRepository) listBasePerformance(ctx context.Context, query Query) ([]Entry, error) {
	const statement = `
WITH facts AS (
    SELECT stats.player_id, profiles.primary_position AS position_code,
           stats.minutes::numeric AS minutes,
           COALESCE(stats.goals,0)::numeric AS goals,
           COALESCE(stats.assists,0)::numeric AS assists,
           COALESCE(stats.shots,0)::numeric AS shots,
           COALESCE(stats.shots_on_target,0)::numeric AS shots_on_target,
           COALESCE(stats.passes_completed,0)::numeric AS passes_completed,
           COALESCE(stats.passes_attempted,0)::numeric AS passes_attempted,
           COALESCE(stats.touches,0)::numeric AS touches,
           COALESCE(stats.duels_won,0)::numeric AS duels_won,
           COALESCE((stats.raw_stats->>'duelLost')::numeric,0) AS duels_lost,
           COALESCE(stats.tackles_won,0)::numeric AS tackles_won,
           COALESCE(stats.interceptions,0)::numeric AS interceptions,
           COALESCE(stats.clearances,0)::numeric AS clearances,
           COALESCE(stats.recoveries,0)::numeric AS recoveries,
           COALESCE(stats.fouls_suffered,0)::numeric AS fouls_suffered,
           COALESCE(stats.dribbles_completed,0)::numeric AS dribbles_completed,
           COALESCE(stats.dribbles_attempted,0)::numeric AS dribbles_attempted,
           COALESCE((stats.raw_stats->>'possessionLostCtrl')::numeric,0) AS possession_lost,
           COALESCE((stats.raw_stats->>'totalProgression')::numeric,0) AS progression,
           COALESCE((stats.raw_stats->>'progressiveBallCarriesCount')::numeric,0) AS progressive_carries,
           COALESCE((stats.raw_stats->>'keyPass')::numeric,0) AS key_passes,
           COALESCE((stats.raw_stats->>'outfielderBlock')::numeric,0) AS blocks,
           COALESCE((stats.raw_stats->>'errorLeadToAShot')::numeric,0)
             + 2*COALESCE((stats.raw_stats->>'errorLeadToAGoal')::numeric,0) AS errors
    FROM player_match_stat stats
    JOIN football_match matches ON matches.id=stats.match_id
    JOIN player_edition_profile profiles
      ON profiles.edition_id=matches.edition_id AND profiles.player_id=stats.player_id
    WHERE matches.edition_id=$1::uuid AND matches.status='finished'
      AND stats.minutes>0 AND profiles.primary_position IS NOT NULL
),
aggregates AS (
    SELECT player_id,max(position_code) AS position_code,count(*)::bigint AS appearances,
           sum(minutes) AS minutes,sum(goals) AS goals,sum(assists) AS assists,
           sum(shots) AS shots,sum(shots_on_target) AS shots_on_target,
           sum(passes_completed) AS passes_completed,sum(passes_attempted) AS passes_attempted,
           sum(touches) AS touches,sum(duels_won) AS duels_won,sum(duels_lost) AS duels_lost,
           sum(tackles_won) AS tackles_won,sum(interceptions) AS interceptions,
           sum(clearances) AS clearances,sum(recoveries) AS recoveries,
           sum(fouls_suffered) AS fouls_suffered,
           sum(dribbles_completed) AS dribbles_completed,sum(dribbles_attempted) AS dribbles_attempted,
           sum(possession_lost) AS possession_lost,
           sum(progression+progressive_carries+2*key_passes) AS progression,
           sum(key_passes) AS key_passes,sum(blocks) AS blocks,sum(errors) AS errors
    FROM facts GROUP BY player_id
),
position_priors AS (
    SELECT position_code,
           sum(passes_completed)/NULLIF(sum(passes_attempted),0) AS pass_rate,
           sum(duels_won)/NULLIF(sum(duels_won+duels_lost),0) AS duel_rate,
           sum(dribbles_completed)/NULLIF(sum(dribbles_attempted),0) AS dribble_rate
    FROM aggregates GROUP BY position_code
),
metrics AS (
    SELECT aggregates.*,
           90*(5*goals+3*assists+0.5*shots_on_target)/minutes AS attack_metric,
           90*progression/minutes AS progression_metric,
           90*(1.5*tackles_won+1.5*interceptions+0.75*blocks+0.5*recoveries
             +0.5*clearances+0.2*duels_won-errors)/minutes AS defence_metric,
           0.50*(passes_completed+50*COALESCE(position_priors.pass_rate,0.5))/NULLIF(passes_attempted+50,0)
             +0.25*(duels_won+20*COALESCE(position_priors.duel_rate,0.5))/NULLIF(duels_won+duels_lost+20,0)
             +0.25*(dribbles_completed+10*COALESCE(position_priors.dribble_rate,0.5))/NULLIF(dribbles_attempted+10,0)
             -0.25*possession_lost/NULLIF(touches,0) AS control_metric,
           90*(touches+4*key_passes+2*shots+2*fouls_suffered)/minutes AS influence_metric
    FROM aggregates JOIN position_priors USING(position_code)
),
inputs AS (
    SELECT position_code,player_id,metric,value
    FROM metrics CROSS JOIN LATERAL (VALUES
      ('A'::text,attack_metric),('P',progression_metric),('D',defence_metric),
      ('C',control_metric),('I',influence_metric)
    ) values_by_metric(metric,value)
    WHERE value IS NOT NULL
),
centers AS (
    SELECT position_code,metric,percentile_cont(.5) WITHIN GROUP(ORDER BY value) AS median,
           avg(value) AS mean,stddev_pop(value) AS stddev
    FROM inputs GROUP BY position_code,metric
),
deviations AS (
    SELECT inputs.*,centers.median,centers.mean,centers.stddev,
           abs(inputs.value-centers.median) AS deviation
    FROM inputs JOIN centers USING(position_code,metric)
),
scales AS (
    SELECT position_code,metric,percentile_cont(.5) WITHIN GROUP(ORDER BY deviation) AS mad
    FROM deviations GROUP BY position_code,metric
),
standardized AS (
    SELECT deviations.*,
           CASE WHEN scales.mad>0 THEN (value-median)/(1.4826*scales.mad)
                WHEN stddev>0 THEN (value-mean)/stddev ELSE 0 END AS z
    FROM deviations JOIN scales USING(position_code,metric)
),
position_weights(position_code,a,p,d,c,i) AS (
    VALUES ('GK'::text,$7::numeric,$8::numeric,$9::numeric,$10::numeric,$11::numeric),
           ('DEF',$12::numeric,$13::numeric,$14::numeric,$15::numeric,$16::numeric),
           ('MID',$17::numeric,$18::numeric,$19::numeric,$20::numeric,$21::numeric),
           ('FWD',$22::numeric,$23::numeric,$24::numeric,$25::numeric,$26::numeric)
),
quality_scores AS (
    SELECT standardized.player_id,
           sum(z*CASE metric WHEN 'A' THEN weights.a WHEN 'P' THEN weights.p
             WHEN 'D' THEN weights.d WHEN 'C' THEN weights.c WHEN 'I' THEN weights.i END) AS quality
    FROM standardized JOIN position_weights weights USING(position_code)
    GROUP BY standardized.player_id
),
base_scores AS (
    SELECT metrics.player_id,metrics.position_code,metrics.appearances,metrics.minutes,
           $4::numeric*quality.quality*metrics.minutes/(metrics.minutes+$6::numeric)
             +$5::numeric*sqrt(metrics.minutes/max(metrics.minutes) OVER()) AS raw_score
    FROM metrics JOIN quality_scores quality USING(player_id)
),
display_scores AS (
    SELECT *,round(100000/(1+exp(-raw_score)))::bigint AS points_milli
    FROM base_scores WHERE ($2::text='' OR position_code=$2::text)
),
enriched AS (
    SELECT scores.*,players.display_name,COALESCE(current_team.name,'') AS team_name
    FROM display_scores scores JOIN player players ON players.id=scores.player_id
    LEFT JOIN LATERAL (
        SELECT teams.name FROM player_edition_team membership
        JOIN team teams ON teams.id=membership.team_id
        WHERE membership.edition_id=$1::uuid AND membership.player_id=scores.player_id
        ORDER BY membership.last_seen_at DESC,membership.team_id LIMIT 1
    ) current_team ON true
),
ranked AS (
    SELECT rank() OVER(ORDER BY raw_score DESC) AS competition_rank,
           row_number() OVER(ORDER BY raw_score DESC,player_id DESC) AS ordinal_rank,*
    FROM enriched
)
SELECT competition_rank,ordinal_rank,player_id::text,display_name,team_name,
       position_code,appearances,minutes::bigint,points_milli
FROM ranked ORDER BY ordinal_rank LIMIT $3`

	gk, def := performance.PositionWeights["GK"], performance.PositionWeights["DEF"]
	mid, fwd := performance.PositionWeights["MID"], performance.PositionWeights["FWD"]
	rows, err := r.pool.Query(
		ctx, statement, query.EditionID, query.Position, query.Limit,
		performance.QualityWeight, performance.VolumeWeight, performance.ReliabilityPriorMinutes,
		gk.Attack, gk.Progression, gk.Defence, gk.Control, gk.Influence,
		def.Attack, def.Progression, def.Defence, def.Control, def.Influence,
		mid.Attack, mid.Progression, mid.Defence, mid.Control, mid.Influence,
		fwd.Attack, fwd.Progression, fwd.Defence, fwd.Control, fwd.Influence,
	)
	if err != nil {
		return nil, fmt.Errorf("query base performance leaderboard: %w", err)
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
			return nil, fmt.Errorf("scan base performance leaderboard: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate base performance leaderboard: %w", err)
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
