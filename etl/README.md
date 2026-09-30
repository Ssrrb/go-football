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

# Weekly production flow. RAW_APF_URI must be durable storage (S3, GCS, or a
# mounted persistent volume), not a container-local path. This publishes a new,
# immutable run only after every response and manifest has been fsynced.
go-football-sources run --rounds 5 \
  --runs-root /mnt/football-raw/apf/clausura-2026/runs \
  --storage-uri "s3://football-raw/apf/clausura-2026/runs"

# Normalize and transactionally load only the completed persisted run; this does
# not make network requests. DATABASE_URL is read from the environment or .env.
go-football-load-clausura-2026 --run-dir /mnt/football-raw/apf/clausura-2026/runs/<run-id> --dry-run
go-football-load-clausura-2026 --run-dir /mnt/football-raw/apf/clausura-2026/runs/<run-id>

# Validate and load the persisted SofaScore fixture/details export. This does
# not make network requests and requires the reviewed APF↔SofaScore crosswalk.
go-football-load-sofascore-clausura-2026 --dry-run \
  --snapshot-uri-root "s3://football-raw/sofascore/clausura-2026"
go-football-load-sofascore-clausura-2026 \
  --snapshot-uri-root "s3://football-raw/sofascore/clausura-2026"
```

The loader validates the official APF competition, edition, and regular-stage
UUIDs before writing. It imports all 22 gameweeks and 12 teams, plus players
observed in lineups or player-stat rows of finished matches. APF has no separate
edition roster endpoint, so scheduled matches alone cannot establish a player
roster. Re-running the loader is idempotent and retains immutable raw snapshots.

The SofaScore loader imports every finished match the export contains, currently
the 72 matches of rounds 1–12, including confirmed lineups, player metrics
(including the decimal match rating), team metrics, incidents, venues, and each
persisted JSON response as an immutable snapshot. The scraper caches whole rounds,
so only events whose status is `finished` are loaded and the round count is derived
from the export rather than hard-coded. It resolves provider IDs only through
`data/sources/sofascore-apf-clausura-2026-crosswalk.json`; names are not identity
keys. Missing metrics remain `NULL`, while the complete provider fields remain in
`raw_stats` and `raw_snapshot`.

The crosswalk carries three maps. `teams` and `players` map SofaScore ids onto APF
ids; `matches` maps a SofaScore fixture onto the APF fixture for the same gameweek,
home side and away side. Without `matches` a fixture both providers publish would be
stored twice, because match identity is namespaced per provider while team and player
identity is not. The player map is deliberately many-to-one: providers sometimes
publish more than one profile for one human, so several SofaScore ids may resolve to
one APF player. Two profiles that appear in the same squad are rejected, because
that would list one human twice in one match. `data/sources/crosswalk-rounds-1-4.json`
pins the earlier four-round scope for the historical fixture under
`tests/sofascore_scraper/data`.

`player_match_stat` and `team_match_stat` are keyed by their natural pair rather
than by a provider id, and APF is canonical for those facts. When APF already stored
a fixture, its row survives the upsert and keeps its own id, so SofaScore aliases are
written against the id read back out of the table instead of the id this loader
computed.

Every APF run lives below `runs/<timestamp-and-random-run-id>/` and contains
`discovery.html`, hash-addressed gameweek and match files, and `manifest.json`.
The manifest records source URLs, IDs, timestamps, result counts, SHA-256
hashes, and immutable `body_uri` values. The loader verifies every hash before
normalizing. `raw_snapshot` stores the hash and `body_uri`, not a duplicate
payload; ensure the URI names storage retained for the lifetime of the database
record. The default `tests/...` run root is for local fixtures only.

The adapters and loaders remain dependency-free and call `psql` for database
writes. Loads are transactional and idempotent. Scoring and outbox publishing
remain separate pipeline stages.
