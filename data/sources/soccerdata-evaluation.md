# SoccerData evaluation for Clausura 2026

Validated against the `soccerdata` 1.9.1 checkout in
`tests/soccerdata/` (commit `e120471e424a173e83e8aeeaaf4d954ab9d38ee4`) on
**2026-08-13**.

## Verdict

Do **not** use SoccerData as the extraction dependency or a production fallback
for the 2026 Copa de Primera APF Clausura. Its custom-league configuration only
maps a name to an identifier; it does not ensure that a provider carries the
competition or that its scraper supports the required data. None of the tested
adapters supplied reliable 2026 round-level player performance data.

The project can still be useful as a reference for cache and DataFrame design,
but adopting it would add provider-specific scraping code and dependencies
without replacing the APF adapter.

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
    "Sofascore": "Division Profesional",
    "SoFIFA": "[Paraguay] Primera División",
    "WhoScored": "Paraguay - Division Profesional",
    "season_code": "single-year",
    "season_start": "Jan",
    "season_end": "Dec"
  }
}
```

SoccerData accepted the custom key for ESPN, FBref, SofaScore, SoFIFA, and
WhoScored. This was only configuration validation. Live reads produced the
results below.

## Results

| Adapter | Observed result | Fit for this project |
| --- | --- | --- |
| ESPN | `ESPN("PAR-Primera Division", 2026).read_schedule()` completed, but returned 205 matches dated from 2020-01-17 through 2020-12-30. SoccerData assigned those rows the requested `2026` season rather than validating the upstream season. | Reject. The feed is stale for this identifier, and the silent season mislabelling is unsafe. The adapter also has no event reader; its normalized lineup output is name-based. |
| SofaScore | The custom league was accepted, but `read_leagues()` raised `KeyError` because `Division Profesional` was absent from the provider's default-tournament list. Direct requests for known tournament `11541`, season `87613` returned HTTP 403 in this environment. | Reject. This SoccerData version implements only league, season, table, and schedule reads for SofaScore—no lineup, event, or player-stat methods. |
| FBref | Direct source probes returned a Cloudflare HTTP 403 response. The adapter relies on browser automation and custom competitions are explicitly unsupported on a best-effort basis. | Reject for live production. Coverage and 2026 player-match data could not be validated, and browser scraping is a poor near-live dependency. |
| WhoScored | Direct source probes returned a Cloudflare HTTP 403 response. The adapter relies on browser automation. | Reject unless licensed access and competition coverage are established separately. |
| SoFIFA | Direct source probes returned HTTP 403. Its data represents EA Sports FC ability ratings rather than actual match performance. | Not applicable to round scoring. |
| MatchHistory | Provides results, betting odds, and limited match statistics rather than player-match performance. | Not applicable. |
| Understat | Provider coverage is focused on selected European leagues. | Competition unavailable. |
| ClubElo | Provides team-level Elo data, primarily for European clubs. | Not applicable. |

The access failures are observations from this environment, not a suggestion to
circumvent provider controls. SoccerData's Apache license does not grant rights
to scrape its upstream websites.

## Comparison with the APF adapter

During the same validation, the official APF page returned HTTP 200 and the
included adapter discovered the 2026 edition, all 22 gameweeks, and six matches
in the selected gameweek. APF match details expose the lineups, events, team
statistics, per-player Opta metrics, and stable UUIDs needed by the data model.
The APF routes therefore remain the technically complete canonical source,
subject to the permission and `robots.txt` caveat documented in
[`extraction.md`](extraction.md).

## Commands used

```bash
# Install/run the checked-out SoccerData package in its own environment.
uv run --project tests/soccerdata python <probe-script>

# Verify the project's deterministic adapter tests.
PYTHONPATH=src python3 -m unittest discover -s tests -v

# Verify live APF discovery.
PYTHONPATH=src python3 -m go_football_sources.cli discover
```

Live provider behavior can change. Re-evaluate only if a licensed feed becomes
available or SoccerData adds an APF-specific adapter with stable IDs and
player-match statistics.
