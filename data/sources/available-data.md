# Available player data

The official APF match payload contains lineups, substitutions, events, team
statistics, and per-player statistics. The extractor keeps the entire original
`stats` object and also exposes a stable `canonical` subset.

## Canonical player fields

| Canonical field | APF/Opta field | Availability |
| --- | --- | --- |
| `minutes` | `minsPlayed` | Yes |
| `goals` | `goals` | Yes |
| `assists` | `goalAssist` | Yes |
| `yellow_cards` | `yellowCard` | Yes |
| `red_cards` | `redCard` | Yes |
| `saves` | `saves` | Goalkeepers |
| `saves_inside_box` | `savedIbox` | Goalkeepers |
| `passes_completed` | `accuratePass` | Yes |
| `passes_attempted` | `totalPass` | Yes |
| `shots` | `totalScoringAtt` | Yes |
| `shots_on_target` | `ontargetScoringAtt` | Yes |
| `touches` | `touches` | Yes |
| `duels_won` | `duelWon` | Yes |
| `tackles_won` | `wonTackle` | Yes |
| `tackles_attempted` | `totalTackle` | Yes |
| `interceptions` | `interceptionWon` | Yes |
| `clearances` | `totalClearance` | Yes |
| `recoveries` | `ballRecovery` | Yes |
| `fouls_committed` | `fouls` | Yes |
| `fouls_suffered` | `wasFouled` | Yes |
| `dribbles_completed` | `wonContest` | Yes |
| `dribbles_attempted` | `totalContest` | Yes |
| `crosses_completed` | `accurateCross` | When applicable |
| `crosses_attempted` | `totalCross` | When applicable |
| `long_balls_completed` | `accurateLongBalls` | When applicable |
| `long_balls_attempted` | `totalLongBalls` | When applicable |
| `offsides` | `totalOffside` | When applicable |
| `dispossessed` | `dispossessed` | When applicable |

An absent field means APF did not publish that metric for that player; it must
not be silently converted to zero. Percentages should be derived from the
completed and attempted counts.

## Additional raw data

The raw payload contains many more Opta metrics, including aerial duels, blocked
shots, penalty actions, goalkeeper claims, defensive actions, pass zones, set
pieces, and possession actions. These fields remain in `player_stats[].stats`
so a scoring model can use them later without another extraction migration.

Lineups and substitutions are separate from player statistics:

- `lineups[]`: player UUID, team UUID, starter, captain, shirt number, manager,
  and formation position.
- `events[]`: event UUID, event/qualification type, team and player UUIDs,
  period, minute/second, and score state.
