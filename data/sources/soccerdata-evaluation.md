# SoccerData evaluation for Clausura 2026

Validated against the `soccerdata` 1.9.1 checkout in
`tests/soccerdata/` (commit `e120471e424a173e83e8aeeaaf4d954ab9d38ee4`) on
**2026-08-13**.

## Verdict

Do **not** use SoccerData unmodified as the extraction dependency or production
fallback for the 2026 Copa de Primera APF Clausura. Its custom-league
configuration only maps names; the SofaScore adapter's discovery route does not
list this competition, and the adapter does not implement match lineups,
incidents, or player statistics.

A second test using SofaScore tournament `11541` and season `87613` from the
provided tournament URL changed the narrower SofaScore conclusion: once the ID
is pinned, SoccerData's HTTP client can retrieve the complete 2026 schedule,
standings, and rich underlying match-detail payloads. The repository is
therefore useful as a starting point for an **optional, licensed validation
adapter**, but it remains neither a drop-in solution nor a replacement for the
official APF adapter.

## Required data

The leaderboard needs current fixtures and results, gameweeks, stable player and
team identifiers, positions and minutes, events, and per-player match metrics.
The source must also be suitable for repeated near-live reads and correction
handling.

## Test setup

A temporary `SOCCERDATA_DIR/config/league_dict.json` registered a calendar-year
league with these candidate mappings:

```json
{
  "PAR-Primera Division": {
    "ESPN": "par.1",
    "FBref": "Primera División",
    "Sofascore": "Primera Division, Clausura",
    "SoFIFA": "[Paraguay] Primera División",
    "WhoScored": "Paraguay - Division Profesional",
    "season_code": "single-year",
    "season_start": "Jul",
    "season_end": "Dec"
  }
}
```

SoccerData accepted the custom key for ESPN, FBref, SofaScore, SoFIFA, and
WhoScored. This was only configuration validation. Even with the exact
SofaScore name, `read_leagues()` could not find the competition because it only
searches SofaScore's default-tournament catalog. A test subclass then supplied
the known tournament ID directly while leaving SoccerData's season, table, and
schedule parsers unchanged.

## Results

| Adapter | Observed result | Fit for this project |
| --- | --- | --- |
| ESPN | `ESPN("PAR-Primera Division", 2026).read_schedule()` completed, but returned 205 matches dated from 2020-01-17 through 2020-12-30. SoccerData assigned those rows the requested `2026` season rather than validating the upstream season. | Reject. The feed is stale for this identifier, and the silent season mislabelling is unsafe. The adapter also has no event reader; its normalized lineup output is name-based. |
| SofaScore | Name-based discovery raised `KeyError`, but pinning tournament `11541` found season `87613`. The unchanged parsers returned a 12-team table and 131 schedule rows across 22 rounds. The raw feed contained all 132 matches; SoccerData silently omitted one postponed match because `read_schedule()` only accepts status codes 0 and 100. | Useful only after extension. This version implements league, season, table, and schedule readers, but no lineup, incident, team-stat, or player-stat readers. |
| FBref | Direct source probes returned a Cloudflare HTTP 403 response. The adapter relies on browser automation and custom competitions are explicitly unsupported on a best-effort basis. | Reject for live production. Coverage and 2026 player-match data could not be validated, and browser scraping is a poor near-live dependency. |
| WhoScored | Direct source probes returned a Cloudflare HTTP 403 response. The adapter relies on browser automation. | Reject unless licensed access and competition coverage are established separately. |
| SoFIFA | Direct source probes returned HTTP 403. Its data represents EA Sports FC ability ratings rather than actual match performance. | Not applicable to round scoring. |
| MatchHistory | Provides results, betting odds, and limited match statistics rather than player-match performance. | Not applicable. |
| Understat | Provider coverage is focused on selected European leagues. | Competition unavailable. |
| ClubElo | Provides team-level Elo data, primarily for European clubs. | Not applicable. |

### SofaScore match-detail retest

Using SoccerData's normal HTTP client, round 4 returned six finished matches. A
finished match (`16588791`, Rubio Ñu 1–3 Recoleta FC) returned:

- event identity and status;
- 47 all-match team-stat fields, including possession, expected goals, shots,
  passes, duels, and average rating;
- 24 incidents covering goals, cards, substitutions, periods, and injury time;
- 23 players per team in the lineup payload, with numeric player IDs, minutes,
  SofaScore rating, and detailed goalkeeper/outfield metrics.

This proves the upstream SofaScore data is technically useful for the scoring
project. It does **not** prove production reliability: ordinary direct requests
returned HTTP 403 in this environment, the successful client uses
SoccerData's TLS request implementation, and these are undocumented internal
website endpoints. Access permission and a stable contract are still required.
SoccerData's Apache license does not grant rights to scrape its upstream sites.

## Comparison with the APF adapter

During the same validation, the official APF page returned HTTP 200 and the
included adapter discovered the 2026 edition, all 22 gameweeks, and six matches
in the selected gameweek. APF match details expose the lineups, events, team
statistics, per-player Opta metrics, and stable UUIDs needed by the data model.
The APF routes therefore remain the technically complete canonical source,
subject to the permission and `robots.txt` caveat documented in
[`extraction.md`](extraction.md). If licensed SofaScore access is obtained, add
a dedicated adapter, map its numeric team/player/match IDs through the alias
model, retain postponed/live statuses, and use it for cross-source validation.

## Commands used

```bash
# Install/run the checked-out SoccerData package in its own environment.
uv run --project tests/soccerdata python <probe-script>

# Verify the project's deterministic adapter tests.
PYTHONPATH=etl/src python3 -m unittest discover -s etl/tests -v

# Verify live APF discovery.
PYTHONPATH=etl/src python3 -m go_football_sources.cli discover
```

Live provider behavior can change. Re-evaluate only if a licensed feed becomes
available or SoccerData adds an APF-specific adapter with stable IDs and
player-match statistics.
