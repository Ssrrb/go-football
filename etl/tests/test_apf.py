import json
import unittest

from go_football_sources.apf import (
    APFClient,
    APFError,
    canonical_player_stats,
    normalize_match_payload,
    parse_next_data,
)


class NextDataTests(unittest.TestCase):
    def test_parses_next_data(self):
        result = parse_next_data(
            '<html><script id="__NEXT_DATA__" type="application/json">'
            '{"props":{"pageProps":{"ok":true}}}'
            "</script></html>"
        )
        self.assertTrue(result["props"]["pageProps"]["ok"])

    def test_rejects_missing_next_data(self):
        with self.assertRaises(APFError):
            parse_next_data("<html></html>")

    def test_discovers_metadata_from_nested_live_shape(self):
        module = {
            "type": "football_list",
            "subtype": "football_competition_match",
            "data": {
                "editionId": "edition-1",
                "roundId": "round-1",
                "gameweeks": [{"id": "week-5", "name": "Jornada 5", "week": 5}],
                "rounds": [
                    {
                        "id": "round-1",
                        "currentGameweek": {"id": "week-5", "week": 5},
                    }
                ],
                "matches": [
                    {
                        "competition": {"id": "competition-1", "name": "Clausura"},
                        "edition": {"id": "edition-1", "name": "2026"},
                        "gameweek": {"id": "week-5", "week": 5},
                    }
                ],
            },
        }
        document = json.dumps({"props": {"pageProps": {"modules": [module]}}})

        class FixtureClient(APFClient):
            def _get(self, path, params=None):
                html = f'<script id="__NEXT_DATA__">{document}</script>'
                return html.encode(), "https://example.test/fixtures"

        result = FixtureClient(delay=0).discover()
        self.assertEqual(result["competition"]["id"], "competition-1")
        self.assertEqual(result["edition"]["id"], "edition-1")
        self.assertEqual(result["current_gameweek"]["id"], "week-5")


class NormalizationTests(unittest.TestCase):
    def test_maps_only_present_stats(self):
        result = canonical_player_stats(
            {"minsPlayed": 90, "goalAssist": 1, "accuratePass": 12, "other": 7}
        )
        self.assertEqual(
            result, {"minutes": 90, "assists": 1, "passes_completed": 12}
        )

    def test_preserves_raw_player_stats_and_ids(self):
        payload = {
            "data": {
                "game": {
                    "id": "match-1",
                    "slug": "home-v-away-1",
                    "homeTeam": {"id": "team-h", "name": "Home"},
                    "awayTeam": {"id": "team-a", "name": "Away"},
                    "homeScore": 2,
                    "awayScore": 1,
                },
                "stats": {
                    "homeTeamStats": {"stats": {"goals": 2}},
                    "awayTeamStats": {"stats": {"goals": 1}},
                    "homeTeamPlayersStats": [
                        {
                            "id": "row-1",
                            "player": {"id": "player-1", "name": "One"},
                            "stats": {"minsPlayed": 90, "goals": 2, "unknownMetric": 4},
                        }
                    ],
                    "awayTeamPlayersStats": [],
                },
                "lineups": {
                    "homeTeam": [
                        {
                            "id": "lineup-1",
                            "player": {"id": "player-1", "name": "One"},
                            "starting": True,
                            "shirtNumber": 9,
                        }
                    ],
                    "awayTeam": [],
                },
                "events": {"total": 1, "data": [{"id": "event-1", "type": "Goal"}]},
            }
        }
        result = normalize_match_payload(payload)
        self.assertEqual(result["match"]["teams"]["home"]["id"], "team-h")
        self.assertEqual(result["player_stats"][0]["team_id"], "team-h")
        self.assertEqual(result["player_stats"][0]["canonical"]["goals"], 2)
        self.assertEqual(result["player_stats"][0]["stats"]["unknownMetric"], 4)
        self.assertEqual(result["lineups"][0]["player"]["id"], "player-1")
        self.assertEqual(result["events"][0]["id"], "event-1")


if __name__ == "__main__":
    unittest.main()
