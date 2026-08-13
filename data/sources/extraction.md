# Data-source extraction decision

Validated against the live sites on **2026-08-13**.

## Decision

Use the official APF site as the canonical source. Its current site is backed by
Opta/LaLiga Maker and contains the fixtures, results, lineups, events, team
statistics, and per-player statistics required by this project. Use SofaScore
only as an independent validation feed if access is licensed. Keep
PlaymakerStats as a manual investigation/QA source rather than an unattended
production dependency.

| Source | What was confirmed | Access/reliability | Role |
| --- | --- | --- | --- |
| APF | 22 gameweeks, fixtures, results, stable UUIDs, lineups, events, team and player stats | No authentication on the website routes; not a documented public API | Primary/canonical |
| SofaScore | Live results, tables, lineups and detailed stats | Undocumented internal API; direct requests returned HTTP 403, while a SoccerData client retest returned data | Optional licensed validation only |
| PlaymakerStats | Live match pages, lineups, events and player stats; numeric entity IDs | Cloudflare challenge returned HTTP 403; no documented API found | Manual QA only |

This avoids a difficult and error-prone cross-site player-name merge. APF UUIDs
become the application's canonical source IDs. If another source is added,
store its IDs in an alias table; never join people or teams by display name.

## APF extraction

### 1. Discover the active edition and gameweeks

Fetch:

```text
GET https://www.apf.org.py/partidos-clausura
```

Parse the JSON inside:

```html
<script id="__NEXT_DATA__" type="application/json">...</script>
```

Recursively find the module whose `type` is `football_list` and `subtype` is
`football_competition_match`. Its `data` object contains `editionId`, `roundId`,
the complete `gameweeks` array, and the currently selected gameweek's matches.
Do not hard-code an edition or gameweek UUID: rediscover them when the process
starts and once per day.

IDs found for Clausura 2026 during validation:

| Entity | ID |
| --- | --- |
| Competition | `d0d19bb5-d510-4231-b2f5-74996bf0b2ef` |
| Edition | `8b239701-bd44-4d87-8a7b-19ba464d44e5` |
| Regular round | `bf0d0c95-cb0d-47e7-8ea9-f9dfb9b11a9d` |

### 2. Fetch one gameweek

The APF frontend uses this JSON route:

```text
GET /api/football_list/football_competition_match/variant_a
    ?editionId={edition UUID}
    &roundId={round UUID}
    &gameweekId={gameweek UUID}
```

The response contains six matches for a normal Clausura gameweek, including the
match UUID and slug, edition/gameweek/team UUIDs, venue, kickoff, score, and
period. Iterate over the discovered `gameweeks`; do not assume there will always
be exactly 22 or exactly six matches.

### 3. Fetch match details

For each match slug, the APF frontend uses:

```text
GET /api/live/layout/football_match?slug={match slug}
```

The useful response sections are:

- `data.game`: identity, competition, edition, gameweek, teams, venue, time,
  score, period/status, formation, managers, and referees.
- `data.lineups.homeTeam` / `awayTeam`: players, starters, shirt numbers,
  captain and formation position.
- `data.events.data`: goals, cards, substitutions and period transitions.
- `data.stats.homeTeamStats` / `awayTeamStats`: team totals.
- `data.stats.homeTeamPlayersStats` / `awayTeamPlayersStats`: per-player Opta
  metrics.

The match's normal HTML page also embeds these data in `__NEXT_DATA__`, but it is
much larger. The JSON route is the technically efficient option used by the APF
frontend.

### Compliance caveat

These are publicly reachable website routes, not a documented or guaranteed
public API. APF's `robots.txt` disallows `/api/`. Before operating an unattended
production collector, obtain APF/Opta permission or a licensed feed and review
the site's terms. The extractor includes a request delay and identifies itself,
but that is not a substitute for permission. If API access is not approved, use
the allowed APF pages or an official licensed feed instead.

## Included extractor

The dependency-free Python adapter is in
`src/go_football_sources/apf.py`. Run it from the repository root:

```bash
# IDs, all gameweeks, and the current fixture list
PYTHONPATH=src python3 -m go_football_sources.cli discover

# One round's fixtures
PYTHONPATH=src python3 -m go_football_sources.cli round 4

# A round plus detail for finished matches, written atomically
PYTHONPATH=src python3 -m go_football_sources.cli \
  --output data/raw/apf/round-4.json round 4 --details finished

# One match
PYTHONPATH=src python3 -m go_football_sources.cli match \
  temporada-2026-paraguay-primera-division-clausura-4-rubio-nu-vs-recoleta-842

# All 22 gameweeks; details are optional because this increases request volume
PYTHONPATH=src python3 -m go_football_sources.cli \
  --output data/raw/apf/season.json season --details none
```

Install it locally if the console command is preferred:

```bash
python3 -m pip install -e .
go-football-sources discover
```

The output preserves every raw player metric in `player_stats[].stats`, exposes
common fields in `player_stats[].canonical`, records `fetched_at` and
`source_url`, and hashes each upstream payload in `payload_sha256`.

Live validation for gameweek 4 returned:

- 6 matches and 6 match-detail payloads
- 276 player-stat rows
- 288 lineup rows (players, substitutes, and managers as supplied by APF)
- 177 match events

The complete fixture pass returned 22 gameweeks and 132 unique matches.

## Update speed and corrections

All three sites display in-match/live state. The APF payload exposes current
period, match clock, score and events, so it can support near-live polling. The
exact upstream publication latency was not measurable without observing a live
match end-to-end and no inspected source exposes a public correction/version
history. Do not claim a guaranteed latency or that a result will never change.

Recommended polling policy:

| Match state | Poll interval |
| --- | --- |
| More than 2 hours before kickoff | 15 minutes |
| Within 2 hours of kickoff | 2 minutes |
| Live | 15–30 seconds, with backoff on errors/429 |
| Finished | Re-fetch after 5, 30 and 120 minutes, then next day |

Store each material raw response as an immutable snapshot keyed by source,
match UUID, fetch time, and SHA-256. Upsert normalized records by source UUID.
When the hash changes, process the new snapshot and retain the old one: this
creates the correction audit trail that the sources themselves do not publish.

## Identity model

APF uses UUIDs for competition, edition, round, gameweek, match, team, player,
venue, event, and often stat/lineup rows. Persist those UUIDs as source IDs and
retain the slug only for fetching and debugging.

Recommended keys:

```text
source_entity(source, entity_type, source_id, internal_id, first_seen, last_seen)
match(source, source_match_id, edition_id, gameweek_id, ...)
player_match_stat(source, source_match_id, source_player_id, source_team_id, ...)
event(source, source_event_id, source_match_id, ...)
raw_snapshot(source, source_match_id, fetched_at, sha256, body_uri)
```

If SofaScore or PlaymakerStats is later licensed, map its numeric IDs to the
internal entities in `source_entity`. Names, shirt numbers, and team membership
are evidence for a reviewed match—not durable identifiers.

## Secondary-source notes

### SofaScore

The page identifies the competition as unique tournament `11541` and the 2026
season as `87613`. Its website uses internal endpoints shaped like:

```text
/api/v1/unique-tournament/11541/season/87613/events/round/{round}
/api/v1/unique-tournament/11541/season/87613/standings/total
/api/v1/event/{eventId}
/api/v1/event/{eventId}/statistics
/api/v1/event/{eventId}/incidents
/api/v1/event/{eventId}/lineups
```

They are not a documented public contract. Ordinary direct requests returned
HTTP 403 during validation. A follow-up test using SoccerData's standard HTTP
client and the IDs above succeeded: it found all 132 fixtures, a 12-team table,
and match lineups, incidents, team statistics, numeric player IDs, minutes,
and player ratings. SoccerData 1.9.1 does not expose those match-detail routes
through its SofaScore adapter, cannot discover this non-default tournament by
name, and drops statuses other than not-started/finished from its schedule.
See [`soccerdata-evaluation.md`](soccerdata-evaluation.md) for the test evidence.

This confirms technical coverage, not authorized or stable production access.
Do not evade access controls. Seek permission/licensing before building an
adapter; otherwise keep SofaScore as human-visible QA only.

### PlaymakerStats

Canonical URLs expose numeric IDs, for example a match ending in `/12308262`, a
player ending in `/118755`, and a team ending in `/2341`. Live pages appear to
contain lineups, events and player stats, but the site presented a Cloudflare
challenge to automated requests and no documented API was found. Browser DOM
scraping after the challenge would be brittle and unsuitable for the primary
pipeline. Do not use search-engine snippets as an extraction mechanism.
