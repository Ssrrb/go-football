## Project Description
The goal of this repo is to create a real-time leaderboard system for ranking and scoring of jugadores de la Primera División Paraguaya Clausura 2026. Using data from APF official site.

## Project Requirements

Create a real-time leaderboard system that ranks players based on their scores in each round of the tournament. The system should meet the following requirements:

User Authentication: Users should be able to register and log in to the system.
Scoring: Calculate rating points for each player within each round.
Leaderboard Updates: Display a global leaderboard showing the top players ranked by rating points, filtered by position.
Real-Time Updates: Update the leaderboard in real-time as new scores are entered.
Team of the Week Report: Generate a team of the week report for the top players for each round.

Use Redis Sorted Sets

Leaderboard Storage: Use Redis sorted sets to store and manage leaderboards.
Real-Time Updates: Utilize Redis sorted sets for efficient real-time updates and queries.
Rank Queries: Use Redis commands to query player ranks and leaderboard positions.

## Data model

The PostgreSQL source-of-truth model, correction/versioning rules, scoring
projections, and Redis key design are documented in
[`data/data-model.md`](data/data-model.md). The corresponding PostgreSQL schema
is in [`data/db/schema.sql`](data/db/schema.sql).

## Data

The canonical data source is the official APF site. Its current Opta-backed
payload includes fixtures, results, lineups, events, team statistics, and
per-player statistics. See [the extraction decision](data/sources/extraction.md)
for the reliability analysis, endpoint discovery, identifiers, correction
strategy, and compliance caveat.

The [SoccerData](https://github.com/probberechts/soccerdata) project was also tested with a
custom Paraguayan league configuration. It is not suitable as the 2026 data
source: ESPN returned 2020 matches while labelling them as the requested 2026
season, SoccerData's SofaScore adapter could not discover the competition and
has no player-detail readers, and the other providers were unavailable or did
not supply player-match performance. See the [SoccerData evaluation](data/sources/soccerdata-evaluation.md)
for the test matrix and evidence.

Quick source check:

```bash
PYTHONPATH=src python3 -m go_football_sources.cli discover
PYTHONPATH=src python3 -m go_football_sources.cli round 4 --details finished
```

Run the source adapter tests with:

```bash
PYTHONPATH=src python3 -m unittest discover -s tests -v
```

## Stack

- Python
- Go
- Redis
- Htmx
- Psql
- Docker
