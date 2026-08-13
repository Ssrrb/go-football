import unittest
from pathlib import Path

from go_football_sources.load_sofascore_clausura_2026 import (
    APF_COMPETITION_ID,
    APF_EDITION_ID,
    LoadError,
    _canonical_id,
    _stat_value,
    _validate_basic,
    collect,
)


class SofaScoreLoaderTests(unittest.TestCase):
    def test_accepts_expected_finished_match(self):
        _validate_basic(
            {
                "id": 42,
                "tournament": {"uniqueTournament": {"id": 11541}},
                "season": {"id": 87613, "year": "2026"},
                "status": {"type": "finished", "code": 100},
                "roundInfo": {"round": 4},
            },
            "42",
        )

    def test_rejects_another_tournament(self):
        with self.assertRaises(LoadError):
            _validate_basic(
                {
                    "id": 42,
                    "tournament": {"uniqueTournament": {"id": 17}},
                    "season": {"id": 87613, "year": "2026"},
                    "status": {"type": "finished", "code": 100},
                    "roundInfo": {"round": 4},
                },
                "42",
            )

    def test_preserves_missing_stats_as_null(self):
        self.assertIsNone(_stat_value({}, "goals"))
        self.assertEqual(_stat_value({"goals": 0}, "goals"), 0)

    def test_provider_namespaces_do_not_collide(self):
        self.assertNotEqual(
            _canonical_id("apf", "player", "1"),
            _canonical_id("sofascore", "player", "1"),
        )

    def test_checked_in_dataset_is_complete(self):
        repository = Path(__file__).resolve().parents[2]
        data_root = repository / "tests" / "sofascore_scraper" / "data"
        if not data_root.exists():
            self.skipTest("SofaScore fixture checkout is not available")
        dataset = collect(
            data_root,
            repository
            / "data"
            / "sources"
            / "sofascore-apf-clausura-2026-crosswalk.json",
        )
        self.assertEqual(len(dataset["matches"]), 24)
        self.assertEqual(len(dataset["teams"]), 12)
        self.assertEqual(len(dataset["players"]), 334)
        self.assertEqual(len(dataset["lineups"]), 1102)
        self.assertEqual(len(dataset["events"]), 498)
        self.assertEqual(
            sum(stat["canonical"]["yellow_cards"] or 0 for stat in dataset["player_stats"]),
            83,
        )
        self.assertEqual(
            sum(stat["canonical"]["red_cards"] or 0 for stat in dataset["player_stats"]),
            5,
        )
        self.assertEqual(
            dataset["crosswalk"]["players"].keys(), dataset["players"].keys()
        )
        self.assertEqual(APF_COMPETITION_ID, "d0d19bb5-d510-4231-b2f5-74996bf0b2ef")
        self.assertEqual(APF_EDITION_ID, "8b239701-bd44-4d87-8a7b-19ba464d44e5")


if __name__ == "__main__":
    unittest.main()
