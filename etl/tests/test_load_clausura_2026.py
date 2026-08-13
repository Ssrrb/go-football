import unittest

from go_football_sources.load_clausura_2026 import (
    EXPECTED_COMPETITION_ID,
    EXPECTED_EDITION_ID,
    EXPECTED_ROUND_ID,
    LoadError,
    _canonical_id,
    _validate_discovery,
)


class ClausuraScopeTests(unittest.TestCase):
    def test_accepts_only_expected_clausura_2026_discovery(self):
        _validate_discovery(
            {
                "competition": {
                    "id": EXPECTED_COMPETITION_ID,
                    "name": "Paraguay Primera División (Clausura)",
                },
                "edition": {"name": "Temporada 2026"},
                "edition_id": EXPECTED_EDITION_ID,
                "round_id": EXPECTED_ROUND_ID,
            }
        )

    def test_rejects_another_edition(self):
        with self.assertRaises(LoadError):
            _validate_discovery(
                {
                    "competition": {
                        "id": EXPECTED_COMPETITION_ID,
                        "name": "Paraguay Primera División (Clausura)",
                    },
                    "edition": {"name": "Temporada 2025"},
                    "edition_id": "another-edition",
                    "round_id": EXPECTED_ROUND_ID,
                }
            )

    def test_generates_stable_canonical_ids(self):
        self.assertEqual(
            _canonical_id("team", "team-1"),
            _canonical_id("team", "team-1"),
        )
        self.assertNotEqual(
            _canonical_id("team", "team-1"),
            _canonical_id("player", "team-1"),
        )


if __name__ == "__main__":
    unittest.main()
