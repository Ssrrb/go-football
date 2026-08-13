-- Adds edition-scoped team and observed player membership to an existing schema.
BEGIN;

CREATE TABLE IF NOT EXISTS edition_team (
    edition_id       uuid NOT NULL REFERENCES edition(id),
    team_id          uuid NOT NULL REFERENCES team(id),
    last_snapshot_id uuid NOT NULL REFERENCES raw_snapshot(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (edition_id, team_id)
);

CREATE TABLE IF NOT EXISTS player_edition_team (
    edition_id       uuid NOT NULL REFERENCES edition(id),
    player_id        uuid NOT NULL REFERENCES player(id),
    team_id          uuid NOT NULL REFERENCES team(id),
    evidence         jsonb NOT NULL DEFAULT '{}'::jsonb,
    first_seen_at    timestamptz NOT NULL,
    last_seen_at     timestamptz NOT NULL,
    last_snapshot_id uuid NOT NULL REFERENCES raw_snapshot(id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (edition_id, player_id, team_id),
    CHECK (jsonb_typeof(evidence) = 'object'),
    CHECK (first_seen_at <= last_seen_at)
);

CREATE INDEX IF NOT EXISTS player_edition_team_team_idx
    ON player_edition_team (edition_id, team_id, player_id);

COMMIT;
