"""Load APF Clausura 2026 teams and observed players into PostgreSQL."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Any, Mapping
from uuid import UUID, uuid5

from .apf import APFClient, APFError, payload_digest


EXPECTED_COMPETITION_ID = "d0d19bb5-d510-4231-b2f5-74996bf0b2ef"
EXPECTED_EDITION_ID = "8b239701-bd44-4d87-8a7b-19ba464d44e5"
EXPECTED_ROUND_ID = "bf0d0c95-cb0d-47e7-8ea9-f9dfb9b11a9d"
ID_NAMESPACE = UUID("27e65a85-811b-4c82-a7ce-0e8cafbd6d15")
FINISHED_STATUSES = {"fulltime", "finished", "played", "afterextra", "afterpenalties"}


class LoadError(RuntimeError):
    """Raised when source data cannot safely be loaded."""


def _canonical_id(entity_type: str, source_id: str) -> str:
    return str(uuid5(ID_NAMESPACE, f"apf:{entity_type}:{source_id}"))


def _snapshot_id(kind: str, source_id: str, digest: str) -> str:
    return str(uuid5(ID_NAMESPACE, f"apf:snapshot:{kind}:{source_id}:{digest}"))


def _matches(payload: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    matches: Any = payload.get("matches")
    if matches is None and isinstance(payload.get("data"), Mapping):
        matches = payload["data"].get("matches")
    return [item for item in (matches or []) if isinstance(item, Mapping)]


def _status(match: Mapping[str, Any]) -> str:
    value = match.get("status") or match.get("period")
    if isinstance(value, Mapping):
        value = value.get("type") or value.get("name")
    return str(value or "").replace("_", "").replace("-", "").lower()


def _validate_discovery(discovery: Mapping[str, Any]) -> None:
    competition = discovery.get("competition") or {}
    edition = discovery.get("edition") or {}
    actual = (
        str(competition.get("id", "")),
        str(discovery.get("edition_id", "")),
        str(discovery.get("round_id", "")),
    )
    expected = (EXPECTED_COMPETITION_ID, EXPECTED_EDITION_ID, EXPECTED_ROUND_ID)
    names = f"{competition.get('name', '')} {edition.get('name', '')}".lower()
    if actual != expected or "clausura" not in names or "2026" not in names:
        raise LoadError(
            "APF discovery did not identify the approved Clausura 2026 edition; "
            "refusing to load another tournament"
        )


def _raw_round_payload(payload: Mapping[str, Any]) -> dict[str, Any]:
    return {
        key: value
        for key, value in payload.items()
        if key not in {"source_url", "fetched_at", "payload_sha256", "requested_gameweek"}
    }


def collect(client: APFClient) -> dict[str, Any]:
    """Collect all teams and players observed in finished Clausura 2026 matches."""
    discovery = client.discover()
    _validate_discovery(discovery)
    fetched_at = str(discovery["fetched_at"])

    snapshots: list[dict[str, Any]] = []
    gameweeks: list[dict[str, Any]] = []
    teams: dict[str, dict[str, Any]] = {}
    players: dict[str, dict[str, Any]] = {}
    memberships: dict[tuple[str, str], dict[str, Any]] = {}
    finished_matches = 0

    for gameweek in discovery.get("gameweeks") or []:
        if not isinstance(gameweek, Mapping) or not gameweek.get("id"):
            continue
        round_payload = client.get_round(
            EXPECTED_EDITION_ID, EXPECTED_ROUND_ID, str(gameweek["id"])
        )
        raw_round = _raw_round_payload(round_payload)
        digest = str(round_payload.get("payload_sha256") or payload_digest(raw_round))
        round_snapshot_id = _snapshot_id("gameweek", str(gameweek["id"]), digest)
        snapshots.append(
            {
                "id": round_snapshot_id,
                "kind": "gameweek",
                "source_id": str(gameweek["id"]),
                "url": str(round_payload["source_url"]),
                "fetched_at": str(round_payload["fetched_at"]),
                "digest": digest,
                "payload": raw_round,
            }
        )
        gameweeks.append(dict(gameweek))

        for fixture in _matches(round_payload):
            for key in ("homeTeam", "awayTeam"):
                team = fixture.get(key)
                if not isinstance(team, Mapping) or not team.get("id"):
                    continue
                team_id = str(team["id"])
                teams[team_id] = {
                    "source": dict(team),
                    "snapshot_id": round_snapshot_id,
                }

            if _status(fixture) not in FINISHED_STATUSES or not fixture.get("slug"):
                continue
            normalized, raw_match = client.get_match_with_raw(str(fixture["slug"]))
            match = normalized.get("match") or {}
            if str((match.get("edition") or {}).get("id")) != EXPECTED_EDITION_ID:
                raise LoadError("APF returned match details from a different edition")
            match_id = str(match.get("id") or fixture.get("id") or "")
            if not match_id:
                raise LoadError("A finished APF match did not have an id")
            digest = str(normalized["payload_sha256"])
            match_snapshot_id = _snapshot_id("match", match_id, digest)
            snapshots.append(
                {
                    "id": match_snapshot_id,
                    "kind": "match",
                    "source_id": match_id,
                    "url": str(normalized["source_url"]),
                    "fetched_at": str(normalized["fetched_at"]),
                    "digest": digest,
                    "payload": raw_match,
                }
            )
            finished_matches += 1

            for row_type, rows in (
                ("lineup", normalized.get("lineups") or []),
                ("player_stat", normalized.get("player_stats") or []),
            ):
                for row in rows:
                    if not isinstance(row, Mapping) or row.get("manager") is True:
                        continue
                    player = row.get("player")
                    team_id = str(row.get("team_id") or "")
                    if not isinstance(player, Mapping) or not player.get("id") or not team_id:
                        continue
                    player_id = str(player["id"])
                    players[player_id] = dict(player)
                    membership = memberships.setdefault(
                        (player_id, team_id),
                        {
                            "evidence": {"lineup": False, "player_stat": False},
                            "first_seen_at": str(normalized["fetched_at"]),
                            "last_seen_at": str(normalized["fetched_at"]),
                            "snapshot_id": match_snapshot_id,
                        },
                    )
                    membership["evidence"][row_type] = True
                    membership["last_seen_at"] = str(normalized["fetched_at"])
                    membership["snapshot_id"] = match_snapshot_id

    if len(gameweeks) != 22:
        raise LoadError(f"Expected 22 Clausura 2026 gameweeks, received {len(gameweeks)}")
    if not teams or not players:
        raise LoadError("APF did not return teams and observed players")
    return {
        "fetched_at": fetched_at,
        "competition": dict(discovery.get("competition") or {}),
        "edition": dict(discovery.get("edition") or {}),
        "gameweeks": gameweeks,
        "snapshots": snapshots,
        "teams": teams,
        "players": players,
        "memberships": memberships,
        "finished_matches": finished_matches,
    }


def _sql_text(value: Any) -> str:
    if value is None:
        return "NULL"
    if isinstance(value, bool):
        return "TRUE" if value else "FALSE"
    if isinstance(value, (int, float)):
        return str(value)
    return "'" + str(value).replace("\x00", "").replace("'", "''") + "'"


def _sql_json(value: Any) -> str:
    document = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return f"{_sql_text(document)}::jsonb"


def _source_entity_sql(
    entity_type: str,
    source_id: str,
    canonical_id: str,
    source_name: str | None,
    metadata: Mapping[str, Any],
    seen_at: str,
) -> str:
    values = ", ".join(
        (
            "'apf'",
            _sql_text(entity_type),
            _sql_text(source_id),
            _sql_text(canonical_id),
            _sql_text(source_name),
            _sql_json(metadata),
            _sql_text(seen_at),
            _sql_text(seen_at),
        )
    )
    return f"""INSERT INTO source_entity
(source_code, entity_type, source_id, canonical_id, source_name, metadata, first_seen_at, last_seen_at)
VALUES ({values})
ON CONFLICT (source_code, entity_type, source_id) DO UPDATE SET
source_name = EXCLUDED.source_name,
metadata = source_entity.metadata || EXCLUDED.metadata,
last_seen_at = GREATEST(source_entity.last_seen_at, EXCLUDED.last_seen_at);"""


def build_sql(dataset: Mapping[str, Any]) -> str:
    """Build one transactional, idempotent PostgreSQL load."""
    seen_at = str(dataset["fetched_at"])
    competition_id = _canonical_id("competition", EXPECTED_COMPETITION_ID)
    edition_id = _canonical_id("edition", EXPECTED_EDITION_ID)
    stage_id = _canonical_id("stage", EXPECTED_ROUND_ID)
    statements = ["BEGIN;", "SET LOCAL lock_timeout = '15s';"]

    statements.append(
        """INSERT INTO data_source (code, display_name, base_url, is_canonical)
VALUES ('apf', 'Asociación Paraguaya de Fútbol', 'https://www.apf.org.py', true)
ON CONFLICT (code) DO UPDATE SET display_name = EXCLUDED.display_name,
base_url = EXCLUDED.base_url, is_canonical = EXCLUDED.is_canonical;"""
    )
    for snapshot in dataset["snapshots"]:
        statements.append(
            "INSERT INTO raw_snapshot "
            "(id, source_code, endpoint_kind, source_entity_id, source_url, fetched_at, payload_sha256, payload) VALUES ("
            + ", ".join(
                (
                    _sql_text(snapshot["id"]),
                    "'apf'",
                    _sql_text(snapshot["kind"]),
                    _sql_text(snapshot["source_id"]),
                    _sql_text(snapshot["url"]),
                    _sql_text(snapshot["fetched_at"]),
                    _sql_text(snapshot["digest"]),
                    _sql_json(snapshot["payload"]),
                )
            )
            + ") ON CONFLICT (id) DO NOTHING;"
        )

    competition_name = str(dataset["competition"].get("name") or "Paraguay Primera División (Clausura)")
    statements.append(
        f"""INSERT INTO competition (id, name, country_code) VALUES
({_sql_text(competition_id)}, {_sql_text(competition_name)}, 'PY')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, updated_at = now();"""
    )
    statements.append(
        f"""INSERT INTO edition (id, competition_id, name, season_year) VALUES
({_sql_text(edition_id)}, {_sql_text(competition_id)}, 'Clausura 2026', 2026)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, season_year = 2026, updated_at = now();"""
    )
    statements.append(
        f"""INSERT INTO tournament_stage (id, edition_id, name, stage_order) VALUES
({_sql_text(stage_id)}, {_sql_text(edition_id)}, 'Regular', 1)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, stage_order = 1, updated_at = now();"""
    )
    statements.extend(
        (
            _source_entity_sql("competition", EXPECTED_COMPETITION_ID, competition_id, competition_name, {}, seen_at),
            _source_entity_sql("edition", EXPECTED_EDITION_ID, edition_id, str(dataset["edition"].get("name") or "Clausura 2026"), {"season_year": 2026}, seen_at),
            _source_entity_sql("stage", EXPECTED_ROUND_ID, stage_id, "Regular", {}, seen_at),
        )
    )

    snapshot_by_gameweek = {
        item["source_id"]: item["id"] for item in dataset["snapshots"] if item["kind"] == "gameweek"
    }
    for gameweek in dataset["gameweeks"]:
        source_id = str(gameweek["id"])
        canonical_id = _canonical_id("gameweek", source_id)
        number = int(gameweek.get("week") or 0)
        if number < 1:
            raise LoadError(f"Gameweek {source_id} did not have a positive week number")
        name = str(gameweek.get("name") or f"Jornada {number}")
        statements.append(
            f"""INSERT INTO gameweek (id, edition_id, stage_id, number, name) VALUES
({_sql_text(canonical_id)}, {_sql_text(edition_id)}, {_sql_text(stage_id)}, {number}, {_sql_text(name)})
ON CONFLICT (id) DO UPDATE SET number = EXCLUDED.number, name = EXCLUDED.name, updated_at = now();"""
        )
        statements.append(
            _source_entity_sql("gameweek", source_id, canonical_id, name, {"week": number}, seen_at)
        )

    for source_id, item in dataset["teams"].items():
        team = item["source"]
        canonical_id = _canonical_id("team", source_id)
        name = str(team.get("name") or team.get("shortName") or source_id)
        metadata = {
            key: team[key]
            for key in ("nickname", "slug", "displayName")
            if team.get(key) is not None
        }
        image = team.get("image")
        if isinstance(image, Mapping) and image.get("url"):
            metadata["image_url"] = image["url"]
        statements.append(
            f"""INSERT INTO team (id, name, short_name, acronym) VALUES
({_sql_text(canonical_id)}, {_sql_text(name)}, {_sql_text(team.get('shortName'))}, {_sql_text(team.get('displayName'))})
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, short_name = EXCLUDED.short_name,
acronym = EXCLUDED.acronym, updated_at = now();"""
        )
        statements.append(
            _source_entity_sql("team", source_id, canonical_id, name, metadata, seen_at)
        )
        statements.append(
            f"""INSERT INTO edition_team (edition_id, team_id, last_snapshot_id) VALUES
({_sql_text(edition_id)}, {_sql_text(canonical_id)}, {_sql_text(item['snapshot_id'])})
ON CONFLICT (edition_id, team_id) DO UPDATE SET
last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"""
        )

    observed_teams: dict[str, list[str]] = {}
    for player_id, team_id in dataset["memberships"]:
        observed_teams.setdefault(player_id, []).append(team_id)
    for source_id, player in dataset["players"].items():
        canonical_id = _canonical_id("player", source_id)
        name = str(player.get("name") or player.get("nickname") or source_id)
        metadata = {
            key: player[key] for key in ("nickname", "slug") if player.get(key) is not None
        }
        metadata["edition_id"] = EXPECTED_EDITION_ID
        metadata["observed_team_ids"] = sorted(set(observed_teams.get(source_id, [])))
        statements.append(
            f"""INSERT INTO player (id, display_name) VALUES
({_sql_text(canonical_id)}, {_sql_text(name)})
ON CONFLICT (id) DO UPDATE SET display_name = EXCLUDED.display_name, updated_at = now();"""
        )
        statements.append(
            _source_entity_sql("player", source_id, canonical_id, name, metadata, seen_at)
        )
        statements.append(
            f"""INSERT INTO player_edition_profile
(edition_id, player_id, primary_position, resolution_method, resolution_note) VALUES
({_sql_text(edition_id)}, {_sql_text(canonical_id)}, NULL, 'unresolved', 'APF match data does not publish a stable primary position')
ON CONFLICT (edition_id, player_id) DO NOTHING;"""
        )

    for (player_source_id, team_source_id), membership in dataset["memberships"].items():
        player_id = _canonical_id("player", player_source_id)
        team_id = _canonical_id("team", team_source_id)
        if team_source_id not in dataset["teams"]:
            raise LoadError(f"Player {player_source_id} referenced unknown team {team_source_id}")
        statements.append(
            f"""INSERT INTO player_edition_team
(edition_id, player_id, team_id, evidence, first_seen_at, last_seen_at, last_snapshot_id) VALUES
({_sql_text(edition_id)}, {_sql_text(player_id)}, {_sql_text(team_id)}, {_sql_json(membership['evidence'])},
 {_sql_text(membership['first_seen_at'])}, {_sql_text(membership['last_seen_at'])}, {_sql_text(membership['snapshot_id'])})
ON CONFLICT (edition_id, player_id, team_id) DO UPDATE SET
evidence = player_edition_team.evidence || EXCLUDED.evidence,
first_seen_at = LEAST(player_edition_team.first_seen_at, EXCLUDED.first_seen_at),
last_seen_at = GREATEST(player_edition_team.last_seen_at, EXCLUDED.last_seen_at),
last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"""
        )

    statements.append("COMMIT;")
    return "\n".join(statements) + "\n"


def _database_url(env_file: Path) -> str:
    value = os.environ.get("DATABASE_URL")
    if value:
        return value
    try:
        lines = env_file.read_text(encoding="utf-8").splitlines()
    except OSError as error:
        raise LoadError(f"Could not read {env_file}") from error
    for raw in lines:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, candidate = line.partition("=")
        if separator and key.removeprefix("export ").strip() == "DATABASE_URL":
            value = candidate.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in {'"', "'"}:
                value = value[1:-1]
            if value:
                return value
    raise LoadError("DATABASE_URL is not set and was not found in the env file")


def load(database_url: str, sql: str) -> None:
    """Execute a generated load transaction through the PostgreSQL client."""
    try:
        result = subprocess.run(
            ["psql", database_url, "-X", "--quiet", "-v", "ON_ERROR_STOP=1"],
            input=sql,
            text=True,
            capture_output=True,
            check=False,
        )
    except OSError as error:
        raise LoadError("Could not execute psql") from error
    if result.returncode:
        message = result.stderr.strip() or "psql failed"
        raise LoadError(message)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=Path, default=Path(".env"))
    parser.add_argument("--delay", type=float, default=0.25)
    parser.add_argument("--dry-run", action="store_true", help="collect and validate without writing")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        dataset = collect(APFClient(delay=args.delay))
        if not args.dry_run:
            load(_database_url(args.env_file), build_sql(dataset))
        summary = {
            "edition": "Clausura 2026",
            "gameweeks": len(dataset["gameweeks"]),
            "finished_matches_scanned": dataset["finished_matches"],
            "teams": len(dataset["teams"]),
            "players": len(dataset["players"]),
            "player_team_memberships": len(dataset["memberships"]),
            "database_loaded": not args.dry_run,
        }
        print(json.dumps(summary, ensure_ascii=False))
        return 0
    except (APFError, LoadError, OSError, ValueError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
