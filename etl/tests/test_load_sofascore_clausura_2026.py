import unittest
from pathlib import Path

from go_football_sources.load_sofascore_clausura_2026 import (
    APF_COMPETITION_ID,
    APF_EDITION_ID,
    LoadError,
    _canonical_id,
    _canonical_player,
    _apf_id,
    _rating_value,
    _sofascore_id,
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

    def test_preserves_decimal_player_rating(self):
        self.assertIsNone(_rating_value({}))
        self.assertEqual(_rating_value({"rating": 7.1}), 7.1)
        with self.assertRaises(LoadError):
            _rating_value({"rating": 10.1})

    def test_provider_namespaces_do_not_collide(self):
        self.assertNotEqual(
            _canonical_id("apf", "player", "1"),
            _canonical_id("sofascore", "player", "1"),
        )

    def test_checked_in_dataset_is_complete(self):
        """The four-round fixture is the historical baseline; it keeps loading."""
        repository = Path(__file__).resolve().parents[2]
        data_root = repository / "tests" / "sofascore_scraper" / "data"
        crosswalk = repository / "data" / "sources" / "crosswalk-rounds-1-4.json"
        if not data_root.exists():
            self.skipTest("SofaScore fixture checkout is not available")
        if not crosswalk.exists():
            self.skipTest("Rounds 1-4 crosswalk snapshot is not available")
        dataset = collect(data_root, crosswalk)
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
        ratings = [
            stat["canonical"]["rating"]
            for stat in dataset["player_stats"]
            if stat["canonical"]["rating"] is not None
        ]
        self.assertEqual(len(ratings), 764)
        self.assertEqual((min(ratings), max(ratings)), (4.8, 9.4))
        self.assertTrue(all(isinstance(rating, float) for rating in ratings))
        self.assertEqual(
            dataset["crosswalk"]["players"].keys(), dataset["players"].keys()
        )
        self.assertEqual(APF_COMPETITION_ID, "d0d19bb5-d510-4231-b2f5-74996bf0b2ef")
        self.assertEqual(APF_EDITION_ID, "8b239701-bd44-4d87-8a7b-19ba464d44e5")

    def test_live_dataset_loads_against_current_crosswalk(self):
        repository = Path(__file__).resolve().parents[2]
        data_root = repository / "tests" / "sofascore_scraper" / "data" / "live"
        crosswalk = repository / "data" / "sources" / "sofascore-apf-clausura-2026-crosswalk.json"
        if not data_root.exists():
            self.skipTest("Live SofaScore export is not available")
        dataset = collect(data_root, crosswalk)
        self.assertEqual(len(dataset["matches"]), 72)
        self.assertEqual(len(dataset["teams"]), 12)
        self.assertEqual(len(dataset["players"]), 395)
        self.assertEqual(len(dataset["lineups"]), 3305)
        self.assertEqual(len(dataset["team_stats"]), 144)
        self.assertEqual(len(dataset["matches"]), len(dataset["team_stats"]) // 2)
        self.assertEqual(
            sorted(dataset["crosswalk"]["players"]), sorted(dataset["players"])
        )

    def test_match_crosswalk_collapses_shared_fixtures(self):
        """A fixture both providers publish must resolve to one internal match."""
        repository = Path(__file__).resolve().parents[2]
        data_root = repository / "tests" / "sofascore_scraper" / "data" / "live"
        if not data_root.exists():
            self.skipTest("Live SofaScore export is not available")
        dataset = collect(
            data_root,
            repository / "data" / "sources" / "sofascore-apf-clausura-2026-crosswalk.json",
        )
        mapping = dataset["crosswalk"]["matches"]
        self.assertTrue(mapping, "the live export should share fixtures with APF")

        # Every match, and every child row, must point at exactly one canonical id.
        by_source = {match["source_id"]: match["id"] for match in dataset["matches"]}
        self.assertEqual(len(set(by_source.values())), len(by_source))
        for source_id, canonical in mapping.items():
            self.assertEqual(by_source[source_id], _apf_id("match", canonical))
        for source_id, canonical in by_source.items():
            if source_id in mapping:
                # Shared fixture: APF namespace, never a SofaScore-only id.
                self.assertNotEqual(canonical, _sofascore_id("match", source_id))
            else:
                self.assertEqual(canonical, _sofascore_id("match", source_id))
        for collection in ("lineups", "player_stats", "team_stats", "events"):
            expected = {by_source[match["source_id"]] for match in dataset["matches"]}
            self.assertEqual(
                {row["match_id"] for row in dataset[collection]} - expected, set(), collection
            )
        # 72 finished fixtures, 48 of which APF also publishes.
        self.assertEqual(len(by_source), 72)
        self.assertEqual(len(set(by_source.values())), 72)
        self.assertEqual(len(mapping), 48)

    def test_crosswalk_aliases_are_squad_disjoint(self):
        """Several SofaScore profiles may alias one APF player, but never in one squad."""
        repository = Path(__file__).resolve().parents[2]
        data_root = repository / "tests" / "sofascore_scraper" / "data" / "live"
        if not data_root.exists():
            self.skipTest("Live SofaScore export is not available")
        dataset = collect(
            data_root,
            repository / "data" / "sources" / "sofascore-apf-clausura-2026-crosswalk.json",
        )
        squads: dict[str, dict[str, set[str]]] = {}
        for lineup in dataset["lineups"]:
            squads.setdefault(lineup["match_source_id"], {}).setdefault(
                _canonical_player(dataset, lineup["player_source_id"]), set()
            ).add(lineup["player_source_id"])
        for match_id, players in squads.items():
            duplicated = {k: sorted(v) for k, v in players.items() if len(v) > 1}
            self.assertEqual(duplicated, {}, f"match {match_id} lists one player twice")


if __name__ == "__main__":
    unittest.main()
