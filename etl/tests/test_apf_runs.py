import json
import tempfile
import unittest
from pathlib import Path

from go_football_sources.apf_runs import capture_run, read_manifest


class _Client:
    def discover_with_raw(self):
        return (
            {
                "edition_id": "edition-1",
                "round_id": "stage-1",
                "gameweeks": [{"id": "week-5", "week": 5}],
                "fetched_at": "2026-08-18T12:00:00Z",
            },
            b"<html>fixture discovery</html>",
            "https://example.test/fixtures",
        )

    def get_round_with_raw(self, edition_id, round_id, gameweek_id):
        self.asserted = (edition_id, round_id, gameweek_id)
        return (
            {"matches": [{"id": "fixture-5", "slug": "home-away", "status": "finished"}],
             "fetched_at": "2026-08-18T12:01:00Z"},
            b'{"matches":[{"id":"fixture-5","slug":"home-away","status":"finished"}]}',
            "https://example.test/round/5",
        )

    def get_match_with_raw_bytes(self, slug):
        payload = {
            "data": {"game": {"id": "match-5", "slug": slug, "edition": {"id": "edition-1"},
                              "homeTeam": {"id": "home", "name": "Home"},
                              "awayTeam": {"id": "away", "name": "Away"}, "homeScore": 1, "awayScore": 0},
                     "stats": {}, "lineups": {}, "events": []}
        }
        body = json.dumps(payload, sort_keys=True).encode()
        return (
            {"match": {"id": "match-5", "edition": {"id": "edition-1"}},
             "fetched_at": "2026-08-18T12:02:00Z"},
            payload,
            body,
            "https://example.test/match/home-away",
        )


class APFRunTests(unittest.TestCase):
    def test_publishes_verified_immutable_run_with_body_uris(self):
        with tempfile.TemporaryDirectory() as temporary:
            run_dir = capture_run(_Client(), Path(temporary), [5], storage_uri="s3://raw/apf/runs")
            manifest = read_manifest(run_dir)
            self.assertEqual(manifest["selected_rounds"], [5])
            self.assertEqual(manifest["result_counts"], {"5": 1})
            self.assertEqual(manifest["gameweek_ids"], {"5": "week-5"})
            self.assertEqual({item["kind"] for item in manifest["responses"]}, {"discovery", "gameweek", "match"})
            self.assertTrue(all(item["body_uri"].startswith("s3://raw/apf/runs/") for item in manifest["responses"]))
            self.assertTrue(all(len(item["sha256"]) == 64 for item in manifest["responses"]))


if __name__ == "__main__":
    unittest.main()
