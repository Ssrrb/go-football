-- Promotes the provider-published player match rating from raw statistics to a
-- queryable canonical metric. Existing SofaScore rows are backfilled safely.
BEGIN;

ALTER TABLE player_match_stat
    ADD COLUMN rating numeric(3,1);

UPDATE player_match_stat
SET rating = (raw_stats ->> 'rating')::numeric(3,1)
WHERE jsonb_typeof(raw_stats -> 'rating') = 'number'
  AND (raw_stats ->> 'rating')::numeric BETWEEN 0 AND 10;

ALTER TABLE player_match_stat
    ADD CONSTRAINT player_match_stat_rating_check
    CHECK (rating IS NULL OR rating BETWEEN 0 AND 10);

COMMIT;
