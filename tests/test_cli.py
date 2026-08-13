import unittest

from go_football_sources.cli import _find_gameweek, _match_status


class GameweekTests(unittest.TestCase):
    def test_finds_apf_week_field(self):
        discovery = {
            "gameweeks": [
                {"id": "one", "name": "Jornada 1", "week": 1},
                {"id": "four", "name": "Jornada 4", "week": 4},
            ]
        }
        self.assertEqual(_find_gameweek(discovery, 4)["id"], "four")

    def test_reads_finished_status_from_period(self):
        self.assertEqual(_match_status({"period": {"type": "FullTime"}}), "fulltime")


if __name__ == "__main__":
    unittest.main()
