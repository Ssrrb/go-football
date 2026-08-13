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
```

The loader validates the official APF competition, edition, and regular-stage
UUIDs before writing. It imports all 22 gameweeks and 12 teams, plus players
observed in lineups or player-stat rows of finished matches. APF has no separate
edition roster endpoint, so scheduled matches alone cannot establish a player
roster. Re-running the loader is idempotent and retains immutable raw snapshots.

The adapter and loader remain dependency-free and call `psql` for database
writes. Scoring and outbox publishing remain separate pipeline stages.
