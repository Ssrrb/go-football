# APF ETL

The ETL package discovers and extracts official APF/Opta payloads. It is an
independent Python 3.11+ application so extraction and normalization can be
deployed on a schedule without coupling Python to the Go web process.

```bash
python3 -m venv .venv
. .venv/bin/activate
python -m pip install -e etl
python -m unittest discover -s etl/tests -v

go-football-sources discover
go-football-sources round 4 --details finished --output data/raw/round-4.json

# Apply data/db/schema.sql first. DATABASE_URL is read from the environment
# or from the repository's .env file.
go-football-load-clausura-2026 --dry-run
go-football-load-clausura-2026

# Validate and load the persisted SofaScore fixture/details export. This does
# not make network requests and requires the reviewed APF↔SofaScore crosswalk.
go-football-load-sofascore-clausura-2026 --dry-run
go-football-load-sofascore-clausura-2026
```

The loader validates the official APF competition, edition, and regular-stage
UUIDs before writing. It imports all 22 gameweeks and 12 teams, plus players
observed in lineups or player-stat rows of finished matches. APF has no separate
edition roster endpoint, so scheduled matches alone cannot establish a player
roster. Re-running the loader is idempotent and retains immutable raw snapshots.

The SofaScore loader imports the 24 finished matches in rounds 1–4, including
confirmed lineups, player metrics, team metrics, incidents, venues, and all 161
persisted JSON responses as immutable snapshots. It resolves provider IDs only
through `data/sources/sofascore-apf-clausura-2026-crosswalk.json`; names are not
identity keys. Missing metrics remain `NULL`, while the complete provider fields
remain in `raw_stats` and `raw_snapshot`.

The adapters and loaders remain dependency-free and call `psql` for database
writes. Loads are transactional and idempotent. Scoring and outbox publishing
remain separate pipeline stages.
