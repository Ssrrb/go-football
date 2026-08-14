package awards

import (
	"context"
	"fmt"

	"github.com/Ssrrb/go-football/internal/performance"
)

// liveCandidates calculates provisional rankings directly from finished-match
// facts. Every performance input is standardized inside the player's position.
// Published selections still take precedence over these live calculations.
func (r *Repository) liveCandidates(ctx context.Context, editionID string, awardCodes []string, limit int) (map[string][]Candidate, error) {
	const statement = `
WITH edition_data AS (
    SELECT editions.starts_on,
           CEIL(MAX(gameweeks.number)::numeric / 2)::integer AS half_cutoff
    FROM edition AS editions
    JOIN gameweek AS gameweeks ON gameweeks.edition_id = editions.id
    WHERE editions.id = $1::uuid
    GROUP BY editions.id
),
facts AS (
    SELECT stats.player_id, stats.team_id, matches.kickoff_at,
           gameweeks.number AS gameweek_number,
           CASE WHEN gameweeks.number <= edition_data.half_cutoff THEN 1 ELSE 2 END AS season_half,
           profiles.primary_position AS position_code, players.date_of_birth,
           COALESCE(stats.minutes, 0)::numeric AS minutes,
           COALESCE(stats.goals, 0)::numeric AS goals,
           COALESCE(stats.assists, 0)::numeric AS assists,
           COALESCE(stats.shots, 0)::numeric AS shots,
           COALESCE(stats.shots_on_target, 0)::numeric AS shots_on_target,
           COALESCE(stats.passes_completed, 0)::numeric AS passes_completed,
           COALESCE(stats.passes_attempted, 0)::numeric AS passes_attempted,
           COALESCE(stats.touches, 0)::numeric AS touches,
           COALESCE(stats.duels_won, 0)::numeric AS duels_won,
           COALESCE((stats.raw_stats ->> 'duelLost')::numeric, 0) AS duels_lost,
           COALESCE((stats.raw_stats ->> 'aerialWon')::numeric, 0) AS aerial_won,
           COALESCE((stats.raw_stats ->> 'aerialLost')::numeric, 0) AS aerial_lost,
           COALESCE(stats.tackles_won, 0)::numeric AS tackles_won,
           COALESCE(stats.tackles_attempted, 0)::numeric AS tackles_attempted,
           COALESCE(stats.interceptions, 0)::numeric AS interceptions,
           COALESCE(stats.clearances, 0)::numeric AS clearances,
           COALESCE(stats.recoveries, 0)::numeric AS recoveries,
           COALESCE(stats.fouls_committed, 0)::numeric AS fouls,
           COALESCE(stats.fouls_suffered, 0)::numeric AS fouls_suffered,
           COALESCE(stats.yellow_cards, 0)::numeric AS yellow_cards,
           COALESCE(stats.red_cards, 0)::numeric AS red_cards,
           COALESCE(stats.dribbles_completed, 0)::numeric AS dribbles_completed,
           COALESCE(stats.dribbles_attempted, 0)::numeric AS dribbles_attempted,
           COALESCE(stats.crosses_completed, 0)::numeric AS crosses_completed,
           COALESCE(stats.crosses_attempted, 0)::numeric AS crosses_attempted,
           COALESCE(stats.long_balls_completed, 0)::numeric AS long_balls_completed,
           COALESCE(stats.long_balls_attempted, 0)::numeric AS long_balls_attempted,
           COALESCE(stats.dispossessed, 0)::numeric AS dispossessed,
           COALESCE(stats.saves, 0)::numeric AS saves,
           COALESCE((stats.raw_stats ->> 'possessionLostCtrl')::numeric, 0) AS possession_lost,
           COALESCE((stats.raw_stats ->> 'totalProgression')::numeric, 0) AS progression,
           COALESCE((stats.raw_stats ->> 'progressiveBallCarriesCount')::numeric, 0) AS progressive_carries,
           COALESCE((stats.raw_stats ->> 'keyPass')::numeric, 0) AS key_passes,
           COALESCE((stats.raw_stats ->> 'outfielderBlock')::numeric, 0) AS blocks,
           COALESCE((stats.raw_stats ->> 'errorLeadToAShot')::numeric, 0)
             + 2*COALESCE((stats.raw_stats ->> 'errorLeadToAGoal')::numeric, 0) AS errors,
           COALESCE((stats.raw_stats ->> 'goodHighClaim')::numeric, 0)
             + COALESCE((stats.raw_stats ->> 'punches')::numeric, 0) AS cross_control,
           COALESCE((stats.raw_stats ->> 'accurateKeeperSweeper')::numeric, 0) AS sweeper_won,
           COALESCE((stats.raw_stats ->> 'totalKeeperSweeper')::numeric, 0) AS sweeper_attempted,
           (lineups.squad_role = 'substitute') AS substitute,
           CASE WHEN stats.team_id = matches.home_team_id
                THEN COALESCE(matches.away_score, 0)
                ELSE COALESCE(matches.home_score, 0) END::numeric AS goals_conceded
    FROM player_match_stat AS stats
    JOIN football_match AS matches ON matches.id = stats.match_id
    JOIN gameweek AS gameweeks ON gameweeks.id = matches.gameweek_id
    JOIN edition_data ON true
    JOIN player AS players ON players.id = stats.player_id
    LEFT JOIN player_edition_profile AS profiles
      ON profiles.edition_id = matches.edition_id
     AND profiles.player_id = stats.player_id
    LEFT JOIN match_lineup AS lineups
      ON lineups.match_id = stats.match_id
     AND lineups.player_id = stats.player_id
    WHERE matches.edition_id = $1::uuid
      AND matches.status = 'finished'
      AND COALESCE(stats.minutes, 0) > 0
),
aggregates AS (
    SELECT player_id,
           (array_agg(team_id ORDER BY kickoff_at DESC))[1] AS team_id,
           max(position_code) AS position_code,
           max(date_of_birth) AS date_of_birth,
           count(*)::bigint AS appearances,
           sum(minutes) AS minutes,
           sum(goals) AS goals, sum(assists) AS assists, sum(shots) AS shots,
           sum(shots_on_target) AS shots_on_target,
           sum(passes_completed) AS passes_completed, sum(passes_attempted) AS passes_attempted,
           sum(touches) AS touches, sum(duels_won) AS duels_won, sum(duels_lost) AS duels_lost,
           sum(aerial_won) AS aerial_won, sum(aerial_lost) AS aerial_lost,
           sum(tackles_won) AS tackles_won, sum(tackles_attempted) AS tackles_attempted,
           sum(interceptions) AS interceptions, sum(clearances) AS clearances,
           sum(recoveries) AS recoveries, sum(fouls) AS fouls,
           sum(fouls_suffered) AS fouls_suffered,
           sum(yellow_cards) AS yellow_cards, sum(red_cards) AS red_cards,
           sum(dribbles_completed) AS dribbles_completed, sum(dribbles_attempted) AS dribbles_attempted,
           sum(crosses_completed) AS crosses_completed, sum(crosses_attempted) AS crosses_attempted,
           sum(long_balls_completed) AS long_balls_completed, sum(long_balls_attempted) AS long_balls_attempted,
           sum(dispossessed) AS dispossessed, sum(possession_lost) AS possession_lost,
           sum(progression + progressive_carries + 2*key_passes) AS progression,
           sum(key_passes) AS key_passes, sum(blocks) AS blocks, sum(errors) AS errors,
           sum(saves) AS saves, sum(goals_conceded) AS goals_conceded,
           sum(cross_control) AS cross_control,
           sum(sweeper_won) AS sweeper_won, sum(sweeper_attempted) AS sweeper_attempted
    FROM facts
    WHERE position_code IS NOT NULL
    GROUP BY player_id
),
position_priors AS (
    SELECT position_code,
           sum(passes_completed)/NULLIF(sum(passes_attempted), 0) AS pass_rate,
           sum(long_balls_completed)/NULLIF(sum(long_balls_attempted), 0) AS long_ball_rate,
           sum(duels_won)/NULLIF(sum(duels_won+duels_lost), 0) AS duel_rate,
           sum(aerial_won)/NULLIF(sum(aerial_won+aerial_lost), 0) AS aerial_rate,
           sum(saves)/NULLIF(sum(saves+goals_conceded), 0) AS save_rate,
           sum(sweeper_won)/NULLIF(sum(sweeper_attempted), 0) AS sweeper_rate,
           sum(dribbles_completed)/NULLIF(sum(dribbles_attempted), 0) AS dribble_rate
    FROM aggregates
    GROUP BY position_code
),
full_metrics AS (
    SELECT aggregates.*,
           90*(5*goals+3*assists+0.5*shots_on_target)/NULLIF(minutes,0) AS attack_metric,
           90*progression/NULLIF(minutes,0) AS progression_metric,
           90*(1.5*tackles_won+1.5*interceptions+0.75*blocks+0.5*recoveries
             +0.5*clearances+0.2*duels_won-errors)/NULLIF(minutes,0) AS defence_metric,
           0.50*(passes_completed+50*COALESCE(position_priors.pass_rate,0.5))/NULLIF(passes_attempted+50,0)
             +0.25*(duels_won+20*COALESCE(position_priors.duel_rate,0.5))/NULLIF(duels_won+duels_lost+20,0)
             +0.25*(dribbles_completed+10*COALESCE(position_priors.dribble_rate,0.5))/NULLIF(dribbles_attempted+10,0)
             -0.25*possession_lost/NULLIF(touches,0) AS control_metric,
           90*(touches+4*key_passes+2*shots+2*fouls_suffered)/NULLIF(minutes,0) AS influence_metric,
           (duels_won+20*COALESCE(position_priors.duel_rate,0.5))/NULLIF(duels_won+duels_lost+20,0) AS duel_rate,
           (aerial_won+15*COALESCE(position_priors.aerial_rate,0.5))/NULLIF(aerial_won+aerial_lost+15,0) AS aerial_rate,
           90*interceptions/NULLIF(minutes,0) AS interceptions_per90,
           90*tackles_won/NULLIF(minutes,0) AS tackles_per90,
           90*blocks/NULLIF(minutes,0) AS blocks_per90,
           90*recoveries/NULLIF(minutes,0) AS recoveries_per90,
           90*(errors+0.1*possession_lost+0.25*fouls+yellow_cards+3*red_cards)/NULLIF(minutes,0) AS errors_per90,
           saves+goals_conceded AS shots_faced,
           (saves+20*COALESCE(position_priors.save_rate,0.7))/NULLIF(saves+goals_conceded+20,0) AS adjusted_save_rate,
           0.6*(passes_completed+50*COALESCE(position_priors.pass_rate,0.5))/NULLIF(passes_attempted+50,0)
             + 0.4*(long_balls_completed+20*COALESCE(position_priors.long_ball_rate,0.5))/NULLIF(long_balls_attempted+20,0) AS distribution_metric,
           90*cross_control/NULLIF(minutes,0) AS cross_control_per90,
           (sweeper_won+10*COALESCE(position_priors.sweeper_rate,0.5))/NULLIF(sweeper_attempted+10,0) AS sweeping_metric,
           -90*errors/NULLIF(minutes,0) AS error_avoidance_metric
    FROM aggregates
    JOIN position_priors USING (position_code)
),
sub_entries AS (
    SELECT events.match_id, events.related_player_id AS player_id,
           events.team_id, COALESCE(events.minute,0) AS entry_minute,
           events.event_order AS entry_order
    FROM match_event AS events
    JOIN football_match AS matches ON matches.id = events.match_id
    WHERE matches.edition_id = $1::uuid AND matches.status = 'finished'
      AND events.is_active AND events.event_type = 'substitution'
      AND events.related_player_id IS NOT NULL
),
sub_impact_actions AS (
    SELECT goals.match_id, goals.player_id, 1.0*goals.leverage AS action_impact
    FROM (
        SELECT events.match_id, events.player_id, events.minute, events.event_order,
               CASE
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = -1 THEN 1.30
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = 0 THEN 1.20
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = 1 THEN 1.00
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) <= -2 THEN 0.90
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = 2 THEN 0.70
                 ELSE 0.50 END::numeric AS leverage
        FROM match_event AS events
        JOIN football_match AS matches ON matches.id=events.match_id
        WHERE matches.edition_id=$1::uuid AND matches.status='finished'
          AND events.is_active AND events.event_type='goal'
          AND COALESCE(events.qualification_type,'') <> 'ownGoal'
          AND events.player_id IS NOT NULL AND events.home_score IS NOT NULL AND events.away_score IS NOT NULL
    ) AS goals
    JOIN sub_entries ON sub_entries.match_id=goals.match_id AND sub_entries.player_id=goals.player_id
                    AND (COALESCE(goals.minute,0)>sub_entries.entry_minute OR
                         (COALESCE(goals.minute,0)=sub_entries.entry_minute AND goals.event_order>sub_entries.entry_order))
    UNION ALL
    SELECT goals.match_id, goals.assist_id, 0.7*goals.leverage
    FROM (
        SELECT events.match_id, events.related_player_id AS assist_id, events.minute, events.event_order,
               CASE
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = -1 THEN 1.30
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = 0 THEN 1.20
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = 1 THEN 1.00
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) <= -2 THEN 0.90
                 WHEN (CASE WHEN events.team_id=matches.home_team_id
                            THEN events.home_score-1-events.away_score
                            ELSE events.away_score-1-events.home_score END) = 2 THEN 0.70
                 ELSE 0.50 END::numeric AS leverage
        FROM match_event AS events
        JOIN football_match AS matches ON matches.id=events.match_id
        WHERE matches.edition_id=$1::uuid AND matches.status='finished'
          AND events.is_active AND events.event_type='goal'
          AND COALESCE(events.qualification_type,'') <> 'ownGoal'
          AND events.related_player_id IS NOT NULL AND events.home_score IS NOT NULL AND events.away_score IS NOT NULL
    ) AS goals
    JOIN sub_entries ON sub_entries.match_id=goals.match_id AND sub_entries.player_id=goals.assist_id
                    AND (COALESCE(goals.minute,0)>sub_entries.entry_minute OR
                         (COALESCE(goals.minute,0)=sub_entries.entry_minute AND goals.event_order>sub_entries.entry_order))
),
sub_aggregates AS (
    SELECT player_id,
           (array_agg(team_id ORDER BY kickoff_at DESC))[1] AS team_id,
           max(position_code) AS position_code,
           count(*)::bigint AS sub_apps, sum(minutes) AS minutes,
           sum(goals) AS goals, sum(assists) AS assists, sum(shots) AS shots,
           sum(shots_on_target) AS shots_on_target,
           sum(passes_completed) AS passes_completed, sum(passes_attempted) AS passes_attempted,
           sum(touches) AS touches, sum(duels_won) AS duels_won, sum(duels_lost) AS duels_lost,
           sum(dribbles_completed) AS dribbles_completed, sum(dribbles_attempted) AS dribbles_attempted,
           sum(tackles_won) AS tackles_won, sum(interceptions) AS interceptions,
           sum(clearances) AS clearances, sum(recoveries) AS recoveries,
           sum(blocks) AS blocks, sum(errors) AS errors,
           sum(fouls_suffered) AS fouls_suffered, sum(possession_lost) AS possession_lost,
           sum(progression+progressive_carries+2*key_passes) AS progression,
           sum(key_passes) AS key_passes
    FROM facts
    WHERE substitute AND position_code IS NOT NULL
    GROUP BY player_id
),
sub_priors AS (
    SELECT position_code,
           sum(passes_completed)/NULLIF(sum(passes_attempted),0) AS pass_rate,
           sum(duels_won)/NULLIF(sum(duels_won+duels_lost),0) AS duel_rate,
           sum(dribbles_completed)/NULLIF(sum(dribbles_attempted),0) AS dribble_rate
    FROM sub_aggregates GROUP BY position_code
),
sub_metrics AS (
    SELECT sub_aggregates.*,
           90*(5*goals+3*assists+0.5*shots_on_target)/NULLIF(minutes,0) AS attack_metric,
           90*progression/NULLIF(minutes,0) AS progression_metric,
           90*(1.5*tackles_won+1.5*interceptions+0.75*blocks+0.5*recoveries
             +0.5*clearances+0.2*duels_won-errors)/NULLIF(minutes,0) AS defence_metric,
           0.50*(passes_completed+30*COALESCE(sub_priors.pass_rate,0.5))/NULLIF(passes_attempted+30,0)
             +0.25*(duels_won+15*COALESCE(sub_priors.duel_rate,0.5))/NULLIF(duels_won+duels_lost+15,0)
             +0.25*(dribbles_completed+8*COALESCE(sub_priors.dribble_rate,0.5))/NULLIF(dribbles_attempted+8,0)
             -0.25*possession_lost/NULLIF(touches,0) AS control_metric,
           90*(touches+4*key_passes+2*shots+2*fouls_suffered)/NULLIF(minutes,0) AS influence_metric,
           COALESCE(impacts.impact,0) AS impact
    FROM sub_aggregates JOIN sub_priors USING(position_code)
    LEFT JOIN (SELECT player_id,sum(action_impact) AS impact FROM sub_impact_actions GROUP BY player_id) impacts USING(player_id)
),
period_aggregates AS (
    SELECT player_id, season_half, max(position_code) AS position_code,
           sum(minutes) AS minutes,
           sum(goals) AS goals, sum(assists) AS assists, sum(shots) AS shots,
           sum(shots_on_target) AS shots_on_target,
           sum(passes_completed) AS passes_completed, sum(passes_attempted) AS passes_attempted,
           sum(touches) AS touches, sum(possession_lost) AS possession_lost,
           sum(duels_won) AS duels_won, sum(duels_lost) AS duels_lost,
           sum(tackles_won) AS tackles_won, sum(tackles_attempted) AS tackles_attempted,
           sum(interceptions) AS interceptions, sum(clearances) AS clearances,
           sum(recoveries) AS recoveries, sum(blocks) AS blocks,
           sum(fouls_suffered) AS fouls_suffered,
           sum(dribbles_completed) AS dribbles_completed, sum(dribbles_attempted) AS dribbles_attempted,
           sum(crosses_completed) AS crosses_completed, sum(crosses_attempted) AS crosses_attempted,
           sum(long_balls_completed) AS long_balls_completed, sum(long_balls_attempted) AS long_balls_attempted,
           sum(progression+progressive_carries+2*key_passes) AS progression,
           sum(key_passes) AS key_passes, sum(errors) AS errors
    FROM facts WHERE position_code IS NOT NULL GROUP BY player_id,season_half
),
period_priors AS (
    SELECT season_half,position_code,
           sum(passes_completed)/NULLIF(sum(passes_attempted),0) AS pass_rate,
           sum(duels_won)/NULLIF(sum(duels_won+duels_lost),0) AS duel_rate,
           sum(dribbles_completed)/NULLIF(sum(dribbles_attempted),0) AS dribble_rate
    FROM period_aggregates GROUP BY season_half,position_code
),
period_metrics AS (
    SELECT period_aggregates.*,
           90*(5*goals+3*assists+0.5*shots_on_target)/NULLIF(minutes,0) AS attack_metric,
           90*progression/NULLIF(minutes,0) AS progression_metric,
           90*(1.5*tackles_won+1.5*interceptions+0.75*blocks+0.5*recoveries
             +0.5*clearances+0.2*duels_won-errors)/NULLIF(minutes,0) AS defence_metric,
           0.50*(passes_completed+50*COALESCE(period_priors.pass_rate,0.5))/NULLIF(passes_attempted+50,0)
             +0.25*(duels_won+20*COALESCE(period_priors.duel_rate,0.5))/NULLIF(duels_won+duels_lost+20,0)
             +0.25*(dribbles_completed+10*COALESCE(period_priors.dribble_rate,0.5))/NULLIF(dribbles_attempted+10,0)
             -0.25*possession_lost/NULLIF(touches,0) AS control_metric,
           90*(touches+4*key_passes+2*shots+2*fouls_suffered)/NULLIF(minutes,0) AS influence_metric,
           90*(passes_completed+tackles_won+dribbles_completed+crosses_completed+long_balls_completed+shots_on_target)/NULLIF(minutes,0) AS success_metric,
           90*((passes_attempted-passes_completed)+(tackles_attempted-tackles_won)
             +(dribbles_attempted-dribbles_completed)+(crosses_attempted-crosses_completed)
             +(long_balls_attempted-long_balls_completed)+(shots-shots_on_target)
             +possession_lost+3*errors)/NULLIF(minutes,0) AS failure_metric
    FROM period_aggregates JOIN period_priors USING(season_half,position_code)
),
position_weights(position_code,a,p,d,c,i) AS (
    VALUES ('GK'::text,$5::numeric,$6::numeric,$7::numeric,$8::numeric,$9::numeric),
           ('DEF',$10::numeric,$11::numeric,$12::numeric,$13::numeric,$14::numeric),
           ('MID',$15::numeric,$16::numeric,$17::numeric,$18::numeric,$19::numeric),
           ('FWD',$20::numeric,$21::numeric,$22::numeric,$23::numeric,$24::numeric)
),
standard_inputs AS (
    SELECT 'quality'::text AS family, position_code AS cohort, player_id, metric, value
    FROM full_metrics CROSS JOIN LATERAL (VALUES
      ('A'::text,attack_metric),('P',progression_metric),('D',defence_metric),('C',control_metric),('I',influence_metric)
    ) AS inputs(metric,value)
    UNION ALL
    SELECT 'defence',position_code,player_id,metric,value FROM full_metrics CROSS JOIN LATERAL (VALUES
      ('duels'::text,duel_rate),('aerial',aerial_rate),('interceptions',interceptions_per90),
      ('tackles',tackles_per90),('blocks',blocks_per90),('recoveries',recoveries_per90),
      ('progression',progression_metric),('errors',errors_per90)
    ) inputs(metric,value) WHERE position_code='DEF'
    UNION ALL
    SELECT 'goalkeeper',position_code,player_id,metric,value FROM full_metrics CROSS JOIN LATERAL (VALUES
      ('shot_stop'::text,adjusted_save_rate),('distribution',distribution_metric),
      ('cross_control',cross_control_per90),('sweeping',sweeping_metric),('error_avoidance',error_avoidance_metric)
    ) inputs(metric,value) WHERE position_code='GK' AND shots_faced>0
    UNION ALL
    SELECT 'sub_quality',position_code,player_id,metric,value FROM sub_metrics CROSS JOIN LATERAL (VALUES
      ('A'::text,attack_metric),('P',progression_metric),('D',defence_metric),('C',control_metric),('I',influence_metric)
    ) inputs(metric,value)
    UNION ALL
    SELECT 'sub_impact',position_code,player_id,'impact',impact FROM sub_metrics
    UNION ALL
    SELECT 'period',position_code||':'||season_half::text,player_id,metric,value FROM period_metrics CROSS JOIN LATERAL (VALUES
      ('A'::text,attack_metric),('P',progression_metric),('D',defence_metric),('C',control_metric),
      ('I',influence_metric),('success',success_metric),('failure',failure_metric)
    ) inputs(metric,value)
),
input_centers AS (
    SELECT family,cohort,metric,percentile_cont(.5) WITHIN GROUP (ORDER BY value) AS median,
           avg(value) AS mean,stddev_pop(value) AS stddev
    FROM standard_inputs WHERE value IS NOT NULL GROUP BY family,cohort,metric
),
input_deviations AS (
    SELECT inputs.*,centers.median,centers.mean,centers.stddev,abs(inputs.value-centers.median) AS deviation
    FROM standard_inputs inputs JOIN input_centers centers USING(family,cohort,metric)
    WHERE inputs.value IS NOT NULL
),
input_scales AS (
    SELECT family,cohort,metric,percentile_cont(.5) WITHIN GROUP (ORDER BY deviation) AS mad
    FROM input_deviations GROUP BY family,cohort,metric
),
standardized AS (
    SELECT input_deviations.*,
           CASE WHEN input_scales.mad>0 THEN (value-median)/(1.4826*input_scales.mad)
                WHEN stddev>0 THEN (value-mean)/stddev ELSE 0 END AS z
    FROM input_deviations JOIN input_scales USING(family,cohort,metric)
),
quality_scores AS (
    SELECT standardized.player_id,
           sum(standardized.z * CASE standardized.metric WHEN 'A' THEN weights.a WHEN 'P' THEN weights.p
             WHEN 'D' THEN weights.d WHEN 'C' THEN weights.c WHEN 'I' THEN weights.i END) AS q
    FROM standardized JOIN full_metrics USING(player_id)
    JOIN position_weights weights ON weights.position_code=full_metrics.position_code
    WHERE family='quality' GROUP BY standardized.player_id
),
defence_scores AS (
    SELECT player_id,sum(z*CASE metric WHEN 'duels' THEN .20 WHEN 'aerial' THEN .15
      WHEN 'interceptions' THEN .20 WHEN 'tackles' THEN .15 WHEN 'blocks' THEN .10
      WHEN 'recoveries' THEN .10 WHEN 'progression' THEN .10 WHEN 'errors' THEN -.10 END) AS def
    FROM standardized WHERE family='defence' GROUP BY player_id
),
goalkeeper_scores AS (
    SELECT standardized.player_id,
           max(z) FILTER(WHERE metric='shot_stop')
             * full_metrics.shots_faced/(full_metrics.shots_faced+100) AS shot_stop,
           max(z) FILTER(WHERE metric='distribution') AS distribution,
           max(z) FILTER(WHERE metric='cross_control') AS cross_control,
           max(z) FILTER(WHERE metric='sweeping') AS sweeping,
           max(z) FILTER(WHERE metric='error_avoidance') AS error_avoidance
    FROM standardized JOIN full_metrics USING(player_id)
    WHERE family='goalkeeper' GROUP BY standardized.player_id,full_metrics.shots_faced
),
sub_scores AS (
    SELECT standardized.player_id,
           sum(z*CASE metric WHEN 'A' THEN weights.a WHEN 'P' THEN weights.p WHEN 'D' THEN weights.d
             WHEN 'C' THEN weights.c WHEN 'I' THEN weights.i END) AS q_sub
    FROM standardized JOIN sub_metrics USING(player_id)
    JOIN position_weights weights ON weights.position_code=sub_metrics.position_code
    WHERE family='sub_quality' GROUP BY standardized.player_id
),
period_scores AS (
    SELECT standardized.player_id,period_metrics.season_half,period_metrics.position_code,period_metrics.minutes,
           sum(z*CASE metric WHEN 'A' THEN weights.a WHEN 'P' THEN weights.p WHEN 'D' THEN weights.d
             WHEN 'C' THEN weights.c WHEN 'I' THEN weights.i ELSE 0 END) AS q,
           max(z) FILTER(WHERE metric='success')-max(z) FILTER(WHERE metric='failure') AS efficiency,
           max(z) FILTER(WHERE metric='I') AS influence
    FROM standardized JOIN period_metrics USING(player_id)
    JOIN position_weights weights ON weights.position_code=period_metrics.position_code
    WHERE family='period' AND standardized.cohort=period_metrics.position_code||':'||period_metrics.season_half::text
    GROUP BY standardized.player_id,period_metrics.season_half,period_metrics.position_code,period_metrics.minutes
),
progress_deltas AS (
    SELECT old.player_id,old.position_code,new.minutes AS new_minutes,
           new.q*new.minutes/(new.minutes+$4::numeric)-old.q*old.minutes/(old.minutes+$4::numeric) AS delta_q,
           new.efficiency-old.efficiency AS delta_efficiency,
           new.influence-old.influence AS delta_influence,
           ln((new.minutes+90)/(old.minutes+90)) AS role_growth
    FROM period_scores old JOIN period_scores new USING(player_id,position_code)
    WHERE old.season_half=1 AND new.season_half=2 AND old.minutes>0 AND new.minutes>0
),
role_centers AS (
    SELECT position_code,percentile_cont(.5) WITHIN GROUP(ORDER BY role_growth) AS median,
           avg(role_growth) AS mean,stddev_pop(role_growth) AS stddev
    FROM progress_deltas GROUP BY position_code
),
role_scales AS (
    SELECT deltas.position_code,percentile_cont(.5) WITHIN GROUP(ORDER BY abs(deltas.role_growth-centers.median)) AS mad
    FROM progress_deltas deltas JOIN role_centers centers USING(position_code) GROUP BY deltas.position_code
),
progress_scores AS (
    SELECT deltas.player_id,deltas.new_minutes,
           .45*deltas.delta_q+.20*deltas.delta_efficiency+.20*deltas.delta_influence+.15*
           CASE WHEN scales.mad>0 THEN (deltas.role_growth-centers.median)/(1.4826*scales.mad)
                WHEN centers.stddev>0 THEN (deltas.role_growth-centers.mean)/centers.stddev ELSE 0 END AS score
    FROM progress_deltas deltas JOIN role_centers centers USING(position_code) JOIN role_scales scales USING(position_code)
),
raw_category_scores AS (
    SELECT 'PLAYER_OF_SEASON'::text AS award_code,metrics.player_id,metrics.team_id,metrics.minutes,
           $25::numeric*quality.q*metrics.minutes/(metrics.minutes+$4::numeric)
             +$26::numeric*sqrt(metrics.minutes/max(metrics.minutes) OVER()) AS raw_score
    FROM full_metrics metrics JOIN quality_scores quality USING(player_id)
    UNION ALL
    SELECT 'DEFENDER_OF_SEASON',metrics.player_id,metrics.team_id,metrics.minutes,
           .35*quality.q*metrics.minutes/(metrics.minutes+$4::numeric)
             +.50*defence.def*metrics.minutes/(metrics.minutes+$4::numeric)
             +.15*sqrt(metrics.minutes/max(metrics.minutes) OVER())
    FROM full_metrics metrics JOIN quality_scores quality USING(player_id)
    JOIN defence_scores defence USING(player_id) WHERE metrics.position_code='DEF'
    UNION ALL
    SELECT 'GOALKEEPER_OF_SEASON',metrics.player_id,metrics.team_id,metrics.minutes,
           .25*quality.q*metrics.minutes/(metrics.minutes+$4::numeric)
             +.35*goalkeepers.shot_stop+.15*goalkeepers.distribution
             +.075*goalkeepers.cross_control+.075*goalkeepers.sweeping
             +.05*goalkeepers.error_avoidance
             +.05*sqrt(metrics.minutes/max(metrics.minutes) OVER())
    FROM full_metrics metrics JOIN quality_scores quality USING(player_id)
    JOIN goalkeeper_scores goalkeepers USING(player_id) WHERE metrics.position_code='GK'
    UNION ALL
    SELECT 'YOUNG_PLAYER_OF_SEASON',metrics.player_id,metrics.team_id,metrics.minutes,
           $25::numeric*quality.q*metrics.minutes/(metrics.minutes+$4::numeric)
             +$26::numeric*sqrt(metrics.minutes/(SELECT max(minutes) FROM full_metrics))
    FROM full_metrics metrics JOIN quality_scores quality USING(player_id) CROSS JOIN edition_data
    WHERE metrics.date_of_birth>edition_data.starts_on-INTERVAL '23 years'
    UNION ALL
    SELECT 'IMPACT_SUBSTITUTE_OF_SEASON',subs.player_id,subs.team_id,subs.minutes,
           .55*impact.z*subs.minutes/(subs.minutes+450)
             +.25*quality.q_sub*subs.minutes/(subs.minutes+450)
             +.20*sqrt(subs.sub_apps/max(subs.sub_apps) OVER())
    FROM sub_metrics subs JOIN sub_scores quality USING(player_id)
    JOIN standardized impact ON impact.player_id=subs.player_id AND impact.family='sub_impact'
    UNION ALL
    SELECT 'MOST_IMPROVED_PLAYER',progress.player_id,metrics.team_id,progress.new_minutes,progress.score
    FROM progress_scores progress JOIN full_metrics metrics USING(player_id)
),
category_scores AS (
    SELECT *,round(100000/(1+exp(-raw_score)))::bigint AS score_milli
    FROM raw_category_scores WHERE raw_score IS NOT NULL
),
ranked AS (
    SELECT category_scores.*,
           row_number() OVER(PARTITION BY award_code ORDER BY raw_score DESC,minutes DESC,player_id)::integer AS candidate_rank
    FROM category_scores WHERE award_code=ANY($2::text[])
)
SELECT ranked.award_code,ranked.candidate_rank,players.id::text,players.display_name,
       COALESCE(teams.name,''),ranked.score_milli
FROM ranked JOIN player players ON players.id=ranked.player_id
LEFT JOIN team teams ON teams.id=ranked.team_id
WHERE ranked.candidate_rank<=$3
ORDER BY ranked.award_code,ranked.candidate_rank`

	gk, def := performance.PositionWeights["GK"], performance.PositionWeights["DEF"]
	mid, fwd := performance.PositionWeights["MID"], performance.PositionWeights["FWD"]
	rows, err := r.pool.Query(
		ctx, statement, editionID, awardCodes, limit, performance.ReliabilityPriorMinutes,
		gk.Attack, gk.Progression, gk.Defence, gk.Control, gk.Influence,
		def.Attack, def.Progression, def.Defence, def.Control, def.Influence,
		mid.Attack, mid.Progression, mid.Defence, mid.Control, mid.Influence,
		fwd.Attack, fwd.Progression, fwd.Defence, fwd.Control, fwd.Influence,
		performance.QualityWeight, performance.VolumeWeight,
	)
	if err != nil {
		return nil, fmt.Errorf("query live season award candidates: %w", err)
	}
	defer rows.Close()

	result := make(map[string][]Candidate, len(awardCodes))
	for rows.Next() {
		var candidate Candidate
		candidate.Projection = true
		if err := rows.Scan(
			&candidate.AwardCode,
			&candidate.Rank,
			&candidate.PlayerID,
			&candidate.PlayerName,
			&candidate.TeamName,
			&candidate.ScoreMilli,
		); err != nil {
			return nil, fmt.Errorf("scan live season award candidate: %w", err)
		}
		result[candidate.AwardCode] = append(result[candidate.AwardCode], candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate live season award candidates: %w", err)
	}
	return result, nil
}
