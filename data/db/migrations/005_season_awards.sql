-- Adds immutable, versioned season-award formulas and auditable selections.
BEGIN;

CREATE TABLE season_award_formula (
    id              uuid PRIMARY KEY,
    award_code      text NOT NULL CHECK (award_code ~ '^[A-Z][A-Z0-9_]+$'),
    name            text NOT NULL,
    version         integer NOT NULL CHECK (version > 0),
    status          text NOT NULL CHECK (status IN ('draft', 'active', 'retired')),
    points_scale    integer NOT NULL DEFAULT 1000 CHECK (points_scale = 1000),
    formula         jsonb NOT NULL,
    published_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (award_code, version),
    UNIQUE (id, award_code),
    CHECK (jsonb_typeof(formula) = 'object'),
    CHECK ((status = 'draft') = (published_at IS NULL))
);

CREATE UNIQUE INDEX season_award_formula_one_active_idx
    ON season_award_formula (award_code) WHERE status = 'active';

-- Published definitions are immutable. Retirement may change status, but any
-- sporting policy change must be inserted as the next formula version.
CREATE FUNCTION protect_published_season_award_formula() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.published_at IS NOT NULL THEN
            RAISE EXCEPTION 'published season award formulas are immutable; retire them instead';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.published_at IS NOT NULL AND (
        NEW.id IS DISTINCT FROM OLD.id OR
        NEW.award_code IS DISTINCT FROM OLD.award_code OR
        NEW.name IS DISTINCT FROM OLD.name OR
        NEW.version IS DISTINCT FROM OLD.version OR
        NEW.points_scale IS DISTINCT FROM OLD.points_scale OR
        NEW.formula IS DISTINCT FROM OLD.formula OR
        NEW.published_at IS DISTINCT FROM OLD.published_at
    ) THEN
        RAISE EXCEPTION 'published season award formulas are immutable; create a new version';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER season_award_formula_immutable
BEFORE UPDATE OR DELETE ON season_award_formula
FOR EACH ROW EXECUTE FUNCTION protect_published_season_award_formula();

CREATE TABLE season_award_selection (
    id                  uuid PRIMARY KEY,
    formula_id          uuid NOT NULL,
    award_code          text NOT NULL,
    edition_id          uuid NOT NULL REFERENCES edition(id),
    selection_version   integer NOT NULL CHECK (selection_version > 0),
    state               text NOT NULL CHECK (state IN ('calculated', 'published', 'superseded', 'failed')),
    is_current          boolean NOT NULL DEFAULT false,
    input_sha256        char(64) NOT NULL CHECK (input_sha256 ~ '^[0-9a-f]{64}$'),
    input_cutoff_at     timestamptz NOT NULL,
    error_message       text,
    calculated_at       timestamptz NOT NULL DEFAULT now(),
    published_at        timestamptz,
    FOREIGN KEY (formula_id, award_code) REFERENCES season_award_formula(id, award_code),
    UNIQUE (edition_id, award_code, selection_version),
    CHECK ((state = 'failed') = (error_message IS NOT NULL)),
    CHECK (NOT is_current OR state = 'published'),
    CHECK ((state IN ('published', 'superseded')) = (published_at IS NOT NULL))
);

CREATE UNIQUE INDEX season_award_selection_one_current_idx
    ON season_award_selection (edition_id, award_code) WHERE is_current;

CREATE TABLE season_award_candidate (
    selection_id        uuid NOT NULL REFERENCES season_award_selection(id) ON DELETE CASCADE,
    player_id           uuid NOT NULL REFERENCES player(id),
    team_id             uuid REFERENCES team(id),
    rank                integer NOT NULL CHECK (rank > 0),
    score_milli         bigint NOT NULL CHECK (score_milli BETWEEN 0 AND 100000),
    minutes             integer NOT NULL CHECK (minutes >= 0),
    breakdown           jsonb NOT NULL,
    PRIMARY KEY (selection_id, player_id),
    UNIQUE (selection_id, rank),
    CHECK (jsonb_typeof(breakdown) = 'object')
);

CREATE INDEX season_award_candidate_ranking_idx
    ON season_award_candidate (selection_id, rank);

INSERT INTO season_award_formula
    (id, award_code, name, version, status, formula, published_at)
VALUES
    ('10000000-0000-4000-8000-000000000001', 'PLAYER_OF_SEASON', 'Jugador de la temporada', 1, 'active',
     $formula${"award":{"aggregation":"weighted_percentiles","code":"PLAYER_OF_SEASON","components":[{"code":"quality","direction":"higher","metric":"minutes-weighted mean provider rating","weight":0.35},{"code":"production","direction":"higher","metric":"common.production_index","weight":0.25},{"code":"influence","direction":"higher","metric":"common.influence","weight":0.2},{"code":"consistency","direction":"higher","metric":"common.consistency","weight":0.1},{"code":"availability","direction":"higher","metric":"common.availability","weight":0.1}],"eligibility":{"edition_state":"finalized","minimum_appearances":10,"minimum_minutes":"max(900, 0.50 * team_possible_minutes)","required_rating_coverage":0.7,"resolved_position_required":true},"id":"10000000-0000-4000-8000-000000000001","name":"Jugador de la temporada","status":"active","tie_breakers":["higher total minutes","higher mean provider rating","more appearances","player UUID ascending"],"version":1},"catalog_version":1,"common_metrics":{"availability":"minutes / sum(team regulation minutes in matches for which the player was registered)","consistency":"0.50 * share(rated matches with rating >= 7.0) + 0.50 * inverse_percentile(MAD(match ratings))","error_index":"0.60 * percentile(1 - success_index) + 0.25 * percentile(dispossessed / touches) + 0.15 * percentile((fouls + 2*yellow_cards + 5*red_cards) / (minutes/90))","goal_contribution_share":"(goals + assists) / team goals while the player was registered","influence":"0.40 * percentile(team_score_share) + 0.30 * percentile(goal_contribution_share) + 0.30 * percentile(result_involvement)","production_index":"per90(5*goals + 3*assists + 0.5*shots_on_target + 0.10*duels_won + 0.15*tackles_won + 0.10*interceptions + 0.05*clearances + 0.05*recoveries + 0.01*passes_completed - 0.20*dispossessed - 0.50*fouls_committed - yellow_cards - 3*red_cards); percentile is calculated within position before cohorts are merged","result_involvement":"minute-weighted team league points / minutes","success_index":"weighted_mean(pass_accuracy:0.35, tackle_accuracy:0.20, dribble_accuracy:0.15, cross_accuracy:0.10, long_ball_accuracy:0.10, shot_accuracy:0.10); category minimum attempts are 50, 5, 5, 5, 10, 5 respectively and available weights are redistributed proportionally","team_score_share":"sum(max(production_index * minutes/90, 0)) / sum(max(all team-player production_index * minutes/90, 0))"},"normalization":{"cohort":"eligible players in the same edition and award","method":"percentile_rank","minimum_cohort_size":10,"missing_data":"required metrics make the player ineligible; optional metric weights are redistributed proportionally only when the formula explicitly allows it","outliers":"winsorize raw rates at cohort percentiles 0.05 and 0.95 before ranking","range":[0,1],"ties":"average_rank"},"specification":"go-football/season-awards"}$formula$::jsonb, '2026-08-14T00:00:00Z');

INSERT INTO season_award_formula
    (id, award_code, name, version, status, formula, published_at)
VALUES
    ('10000000-0000-4000-8000-000000000002', 'DEFENDER_OF_SEASON', 'Defensa de la temporada', 1, 'active',
     $formula${"award":{"aggregation":"weighted_percentiles","code":"DEFENDER_OF_SEASON","components":[{"code":"defensive_output","direction":"higher","metric":"per90(tackles_won + interceptions + 0.5*clearances + 0.5*recoveries)","weight":0.3},{"code":"duel_efficiency","direction":"higher","metric":"0.60*tackle_accuracy + 0.40*duel_win_rate; duel attempts come from raw_stats","weight":0.2},{"code":"team_defence","direction":"higher","metric":"0.50*minute_weighted_clean_sheet_rate + 0.50*inverse(goals_conceded_while_on_pitch_per90)","weight":0.15},{"code":"quality","direction":"higher","metric":"minutes-weighted mean provider rating","weight":0.15},{"code":"influence","direction":"higher","metric":"common.influence within DEF cohort","weight":0.1},{"code":"discipline","direction":"lower","metric":"per90(fouls + 2*yellow_cards + 5*red_cards)","weight":0.1}],"eligibility":{"edition_state":"finalized","minimum_appearances":10,"minimum_minutes":"max(900, 0.50 * team_possible_minutes)","position":"DEF","required_rating_coverage":0.6},"id":"10000000-0000-4000-8000-000000000002","name":"Defensa de la temporada","status":"active","tie_breakers":["lower goals conceded while on pitch per 90","higher total minutes","fewer red cards","player UUID ascending"],"version":1},"catalog_version":1,"common_metrics":{"availability":"minutes / sum(team regulation minutes in matches for which the player was registered)","consistency":"0.50 * share(rated matches with rating >= 7.0) + 0.50 * inverse_percentile(MAD(match ratings))","error_index":"0.60 * percentile(1 - success_index) + 0.25 * percentile(dispossessed / touches) + 0.15 * percentile((fouls + 2*yellow_cards + 5*red_cards) / (minutes/90))","goal_contribution_share":"(goals + assists) / team goals while the player was registered","influence":"0.40 * percentile(team_score_share) + 0.30 * percentile(goal_contribution_share) + 0.30 * percentile(result_involvement)","production_index":"per90(5*goals + 3*assists + 0.5*shots_on_target + 0.10*duels_won + 0.15*tackles_won + 0.10*interceptions + 0.05*clearances + 0.05*recoveries + 0.01*passes_completed - 0.20*dispossessed - 0.50*fouls_committed - yellow_cards - 3*red_cards); percentile is calculated within position before cohorts are merged","result_involvement":"minute-weighted team league points / minutes","success_index":"weighted_mean(pass_accuracy:0.35, tackle_accuracy:0.20, dribble_accuracy:0.15, cross_accuracy:0.10, long_ball_accuracy:0.10, shot_accuracy:0.10); category minimum attempts are 50, 5, 5, 5, 10, 5 respectively and available weights are redistributed proportionally","team_score_share":"sum(max(production_index * minutes/90, 0)) / sum(max(all team-player production_index * minutes/90, 0))"},"normalization":{"cohort":"eligible players in the same edition and award","method":"percentile_rank","minimum_cohort_size":10,"missing_data":"required metrics make the player ineligible; optional metric weights are redistributed proportionally only when the formula explicitly allows it","outliers":"winsorize raw rates at cohort percentiles 0.05 and 0.95 before ranking","range":[0,1],"ties":"average_rank"},"specification":"go-football/season-awards"}$formula$::jsonb, '2026-08-14T00:00:00Z');

INSERT INTO season_award_formula
    (id, award_code, name, version, status, formula, published_at)
VALUES
    ('10000000-0000-4000-8000-000000000003', 'GOALKEEPER_OF_SEASON', 'Portero de la temporada', 1, 'active',
     $formula${"award":{"aggregation":"weighted_percentiles","code":"GOALKEEPER_OF_SEASON","components":[{"code":"goals_prevented","direction":"higher","metric":"post_shot_xg_on_target_faced - goals_conceded; if unavailable use saves - cohort_expected_saves(shots_on_target_faced)","weight":0.3},{"code":"save_percentage","direction":"higher","metric":"saves / shots_on_target_faced","weight":0.25},{"code":"box_control","direction":"higher","metric":"saves_inside_box and successful claims per 90 from raw_stats","weight":0.15},{"code":"clean_sheets","direction":"higher","metric":"minute-weighted clean-sheet rate","weight":0.15},{"code":"distribution","direction":"higher","metric":"0.60*pass_accuracy + 0.40*long_ball_accuracy","weight":0.1},{"code":"discipline_errors","direction":"lower","metric":"errors leading to shot/goal plus card-weighted fouls per 90 from raw_stats/events","weight":0.05}],"eligibility":{"edition_state":"finalized","minimum_appearances":8,"minimum_minutes":"max(720, 0.40 * team_possible_minutes)","minimum_shots_on_target_faced":25,"position":"GK"},"id":"10000000-0000-4000-8000-000000000003","name":"Portero de la temporada","status":"active","tie_breakers":["more goals prevented","higher save percentage","higher total minutes","player UUID ascending"],"version":1},"catalog_version":1,"common_metrics":{"availability":"minutes / sum(team regulation minutes in matches for which the player was registered)","consistency":"0.50 * share(rated matches with rating >= 7.0) + 0.50 * inverse_percentile(MAD(match ratings))","error_index":"0.60 * percentile(1 - success_index) + 0.25 * percentile(dispossessed / touches) + 0.15 * percentile((fouls + 2*yellow_cards + 5*red_cards) / (minutes/90))","goal_contribution_share":"(goals + assists) / team goals while the player was registered","influence":"0.40 * percentile(team_score_share) + 0.30 * percentile(goal_contribution_share) + 0.30 * percentile(result_involvement)","production_index":"per90(5*goals + 3*assists + 0.5*shots_on_target + 0.10*duels_won + 0.15*tackles_won + 0.10*interceptions + 0.05*clearances + 0.05*recoveries + 0.01*passes_completed - 0.20*dispossessed - 0.50*fouls_committed - yellow_cards - 3*red_cards); percentile is calculated within position before cohorts are merged","result_involvement":"minute-weighted team league points / minutes","success_index":"weighted_mean(pass_accuracy:0.35, tackle_accuracy:0.20, dribble_accuracy:0.15, cross_accuracy:0.10, long_ball_accuracy:0.10, shot_accuracy:0.10); category minimum attempts are 50, 5, 5, 5, 10, 5 respectively and available weights are redistributed proportionally","team_score_share":"sum(max(production_index * minutes/90, 0)) / sum(max(all team-player production_index * minutes/90, 0))"},"normalization":{"cohort":"eligible players in the same edition and award","method":"percentile_rank","minimum_cohort_size":10,"missing_data":"required metrics make the player ineligible; optional metric weights are redistributed proportionally only when the formula explicitly allows it","outliers":"winsorize raw rates at cohort percentiles 0.05 and 0.95 before ranking","range":[0,1],"ties":"average_rank"},"specification":"go-football/season-awards"}$formula$::jsonb, '2026-08-14T00:00:00Z');

INSERT INTO season_award_formula
    (id, award_code, name, version, status, formula, published_at)
VALUES
    ('10000000-0000-4000-8000-000000000004', 'YOUNG_PLAYER_OF_SEASON', 'Jugador joven de la temporada (Sub-23)', 1, 'active',
     $formula${"award":{"aggregation":"weighted_percentiles","code":"YOUNG_PLAYER_OF_SEASON","components":[{"code":"quality","direction":"higher","metric":"minutes-weighted mean provider rating","weight":0.35},{"code":"production","direction":"higher","metric":"common.production_index","weight":0.25},{"code":"influence","direction":"higher","metric":"common.influence","weight":0.2},{"code":"consistency","direction":"higher","metric":"common.consistency","weight":0.1},{"code":"availability","direction":"higher","metric":"common.availability","weight":0.1}],"eligibility":{"age_rule":"age < 23 on edition.starts_on; date_of_birth is required","edition_state":"finalized","minimum_appearances":8,"minimum_minutes":"max(540, 0.30 * team_possible_minutes)","required_rating_coverage":0.6,"resolved_position_required":true},"id":"10000000-0000-4000-8000-000000000004","name":"Jugador joven de la temporada (Sub-23)","status":"active","tie_breakers":["younger age on edition start","higher total minutes","higher mean provider rating","player UUID ascending"],"version":1},"catalog_version":1,"common_metrics":{"availability":"minutes / sum(team regulation minutes in matches for which the player was registered)","consistency":"0.50 * share(rated matches with rating >= 7.0) + 0.50 * inverse_percentile(MAD(match ratings))","error_index":"0.60 * percentile(1 - success_index) + 0.25 * percentile(dispossessed / touches) + 0.15 * percentile((fouls + 2*yellow_cards + 5*red_cards) / (minutes/90))","goal_contribution_share":"(goals + assists) / team goals while the player was registered","influence":"0.40 * percentile(team_score_share) + 0.30 * percentile(goal_contribution_share) + 0.30 * percentile(result_involvement)","production_index":"per90(5*goals + 3*assists + 0.5*shots_on_target + 0.10*duels_won + 0.15*tackles_won + 0.10*interceptions + 0.05*clearances + 0.05*recoveries + 0.01*passes_completed - 0.20*dispossessed - 0.50*fouls_committed - yellow_cards - 3*red_cards); percentile is calculated within position before cohorts are merged","result_involvement":"minute-weighted team league points / minutes","success_index":"weighted_mean(pass_accuracy:0.35, tackle_accuracy:0.20, dribble_accuracy:0.15, cross_accuracy:0.10, long_ball_accuracy:0.10, shot_accuracy:0.10); category minimum attempts are 50, 5, 5, 5, 10, 5 respectively and available weights are redistributed proportionally","team_score_share":"sum(max(production_index * minutes/90, 0)) / sum(max(all team-player production_index * minutes/90, 0))"},"normalization":{"cohort":"eligible players in the same edition and award","method":"percentile_rank","minimum_cohort_size":10,"missing_data":"required metrics make the player ineligible; optional metric weights are redistributed proportionally only when the formula explicitly allows it","outliers":"winsorize raw rates at cohort percentiles 0.05 and 0.95 before ranking","range":[0,1],"ties":"average_rank"},"specification":"go-football/season-awards"}$formula$::jsonb, '2026-08-14T00:00:00Z');

INSERT INTO season_award_formula
    (id, award_code, name, version, status, formula, published_at)
VALUES
    ('10000000-0000-4000-8000-000000000005', 'IMPACT_SUBSTITUTE_OF_SEASON', 'Suplente más decisivo de la temporada', 1, 'active',
     $formula${"award":{"aggregation":"weighted_percentiles","code":"IMPACT_SUBSTITUTE_OF_SEASON","components":[{"code":"decisive_actions","direction":"higher","metric":"per90(4*winning_goals + 3*equalizing_goals + 2*other_goals + 1.5*assists); event score state defines decisive actions","weight":0.35},{"code":"score_swing","direction":"higher","metric":"team league-points delta between substitution entry and final whistle, per substitute appearance","weight":0.25},{"code":"performance","direction":"higher","metric":"common.production_index using substitute appearances only","weight":0.2},{"code":"frequency","direction":"higher","metric":"share of substitute appearances with a goal, assist, or positive points swing","weight":0.1},{"code":"reliability","direction":"higher","metric":"min(1, substitute_minutes / 540)","weight":0.1}],"eligibility":{"edition_state":"finalized","maximum_starter_share":0.5,"minimum_substitute_appearances":5,"minimum_substitute_minutes":180,"only_substitute_minutes_count":true},"id":"10000000-0000-4000-8000-000000000005","name":"Suplente más decisivo de la temporada","status":"active","tie_breakers":["more winning plus equalizing goals as substitute","more substitute assists","more substitute minutes","player UUID ascending"],"version":1},"catalog_version":1,"common_metrics":{"availability":"minutes / sum(team regulation minutes in matches for which the player was registered)","consistency":"0.50 * share(rated matches with rating >= 7.0) + 0.50 * inverse_percentile(MAD(match ratings))","error_index":"0.60 * percentile(1 - success_index) + 0.25 * percentile(dispossessed / touches) + 0.15 * percentile((fouls + 2*yellow_cards + 5*red_cards) / (minutes/90))","goal_contribution_share":"(goals + assists) / team goals while the player was registered","influence":"0.40 * percentile(team_score_share) + 0.30 * percentile(goal_contribution_share) + 0.30 * percentile(result_involvement)","production_index":"per90(5*goals + 3*assists + 0.5*shots_on_target + 0.10*duels_won + 0.15*tackles_won + 0.10*interceptions + 0.05*clearances + 0.05*recoveries + 0.01*passes_completed - 0.20*dispossessed - 0.50*fouls_committed - yellow_cards - 3*red_cards); percentile is calculated within position before cohorts are merged","result_involvement":"minute-weighted team league points / minutes","success_index":"weighted_mean(pass_accuracy:0.35, tackle_accuracy:0.20, dribble_accuracy:0.15, cross_accuracy:0.10, long_ball_accuracy:0.10, shot_accuracy:0.10); category minimum attempts are 50, 5, 5, 5, 10, 5 respectively and available weights are redistributed proportionally","team_score_share":"sum(max(production_index * minutes/90, 0)) / sum(max(all team-player production_index * minutes/90, 0))"},"normalization":{"cohort":"eligible players in the same edition and award","method":"percentile_rank","minimum_cohort_size":10,"missing_data":"required metrics make the player ineligible; optional metric weights are redistributed proportionally only when the formula explicitly allows it","outliers":"winsorize raw rates at cohort percentiles 0.05 and 0.95 before ranking","range":[0,1],"ties":"average_rank"},"specification":"go-football/season-awards"}$formula$::jsonb, '2026-08-14T00:00:00Z');

INSERT INTO season_award_formula
    (id, award_code, name, version, status, formula, published_at)
VALUES
    ('10000000-0000-4000-8000-000000000006', 'MOST_IMPROVED_PLAYER', 'Jugador con mayor progresión', 1, 'active',
     $formula${"award":{"aggregation":"weighted_percentile_deltas_centered","code":"MOST_IMPROVED_PLAYER","components":[{"code":"quality_growth","direction":"higher","metric":"late minutes-weighted mean rating - early minutes-weighted mean rating","weight":0.3},{"code":"success_growth","direction":"higher","metric":"late common.success_index - early common.success_index; each rate uses completed/attempted counts","weight":0.2},{"code":"error_reduction","direction":"higher","metric":"early common.error_index - late common.error_index","weight":0.2},{"code":"influence_growth","direction":"higher","metric":"late common.influence - early common.influence","weight":0.2},{"code":"minutes_growth","direction":"higher","metric":"late minutes share - early minutes share","weight":0.1}],"eligibility":{"edition_state":"finalized","minimum_appearances_per_window":4,"minimum_minutes_per_window":270,"minimum_rating_coverage_per_window":0.6,"minimum_success_categories":4,"windows":"with N finalized edition gameweeks and k=ceil(0.35*N), early = first k and late = last k ordered by number; require N >= 10 and non-overlapping windows; middle gameweeks are excluded"},"final_formula":"score = 50 + 50 * reliability * sum(weight_i * (2 * percentile(delta_i) - 1)); clamp to [0,100]","id":"10000000-0000-4000-8000-000000000006","name":"Jugador con mayor progresión","reliability":"min(1, sqrt(min(early_minutes, late_minutes) / 540))","status":"active","tie_breakers":["higher influence growth","higher success growth","more late-window minutes","player UUID ascending"],"version":1},"catalog_version":1,"common_metrics":{"availability":"minutes / sum(team regulation minutes in matches for which the player was registered)","consistency":"0.50 * share(rated matches with rating >= 7.0) + 0.50 * inverse_percentile(MAD(match ratings))","error_index":"0.60 * percentile(1 - success_index) + 0.25 * percentile(dispossessed / touches) + 0.15 * percentile((fouls + 2*yellow_cards + 5*red_cards) / (minutes/90))","goal_contribution_share":"(goals + assists) / team goals while the player was registered","influence":"0.40 * percentile(team_score_share) + 0.30 * percentile(goal_contribution_share) + 0.30 * percentile(result_involvement)","production_index":"per90(5*goals + 3*assists + 0.5*shots_on_target + 0.10*duels_won + 0.15*tackles_won + 0.10*interceptions + 0.05*clearances + 0.05*recoveries + 0.01*passes_completed - 0.20*dispossessed - 0.50*fouls_committed - yellow_cards - 3*red_cards); percentile is calculated within position before cohorts are merged","result_involvement":"minute-weighted team league points / minutes","success_index":"weighted_mean(pass_accuracy:0.35, tackle_accuracy:0.20, dribble_accuracy:0.15, cross_accuracy:0.10, long_ball_accuracy:0.10, shot_accuracy:0.10); category minimum attempts are 50, 5, 5, 5, 10, 5 respectively and available weights are redistributed proportionally","team_score_share":"sum(max(production_index * minutes/90, 0)) / sum(max(all team-player production_index * minutes/90, 0))"},"normalization":{"cohort":"eligible players in the same edition and award","method":"percentile_rank","minimum_cohort_size":10,"missing_data":"required metrics make the player ineligible; optional metric weights are redistributed proportionally only when the formula explicitly allows it","outliers":"winsorize raw rates at cohort percentiles 0.05 and 0.95 before ranking","range":[0,1],"ties":"average_rank"},"specification":"go-football/season-awards"}$formula$::jsonb, '2026-08-14T00:00:00Z');

COMMIT;
