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

from .apf import APFClient, APFError, discovery_from_response, normalize_match_payload, payload_digest
from .apf_runs import gameweek_number, read_manifest


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
    """Collect teams and players straight from the network.

    Retained for reference only. The loader no longer calls it: ``build_sql``
    requires a durable ``body_uri`` for every snapshot, which only a published
    run can supply, so a dataset from this function cannot be loaded. Fetch with
    ``go-football-sources run`` and normalize with ``collect_run`` instead.
    """
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
                "body_uri": None,
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
                    "body_uri": None,
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
        "matches": [],
        "player_stats": [],
        "finished_matches": finished_matches,
    }


def collect_run(run_dir: Path) -> dict[str, Any]:
    """Normalize one verified APF run; this function performs no network I/O."""
    manifest = read_manifest(run_dir)
    records = [dict(item) for item in manifest["responses"]]
    discovery_record = next((item for item in records if item.get("kind") == "discovery"), None)
    if discovery_record is None:
        raise LoadError("APF run has no discovery response")
    try:
        discovery_body = (run_dir / discovery_record["path"]).read_bytes()
        discovery = discovery_from_response(discovery_body, str(discovery_record["source_url"]))
    except (OSError, UnicodeDecodeError, APFError) as error:
        raise LoadError("APF run discovery response is invalid") from error
    _validate_discovery(discovery)
    if (
        str(manifest.get("edition_id")) != EXPECTED_EDITION_ID
        or str(manifest.get("round_id")) != EXPECTED_ROUND_ID
    ):
        raise LoadError("APF run manifest is not for the approved Clausura 2026 edition")

    snapshots: list[dict[str, Any]] = []
    for item in records:
        kind = item.get("kind")
        if kind not in {"discovery", "gameweek", "match"}:
            raise LoadError("APF run has an unsupported response kind")
        source_id = str(item.get("source_entity_id") or "")
        digest = str(item.get("sha256") or "")
        uri = item.get("body_uri")
        if not source_id or len(digest) != 64 or not isinstance(uri, str) or not uri:
            raise LoadError("APF run response is missing immutable snapshot metadata")
        snapshots.append({
            "id": _snapshot_id(kind, source_id, digest),
            "kind": kind,
            "source_id": source_id,
            "url": str(item.get("source_url") or ""),
            "fetched_at": str(item.get("fetched_at") or manifest["finished_at"]),
            "digest": digest,
            "body_uri": uri,
        })

    gameweek_by_number = {
        gameweek_number(item): dict(item)
        for item in discovery["gameweeks"]
        if isinstance(item, Mapping)
    }
    selected = [int(number) for number in manifest.get("selected_rounds") or []]
    if not selected or any(number not in gameweek_by_number for number in selected):
        raise LoadError("APF run selected rounds do not match discovery")
    gameweeks = [gameweek_by_number[number] for number in sorted(selected)]
    snapshot_by_source = {snapshot["source_id"]: snapshot["id"] for snapshot in snapshots}
    teams: dict[str, dict[str, Any]] = {}
    players: dict[str, dict[str, Any]] = {}
    memberships: dict[tuple[str, str], dict[str, Any]] = {}
    matches: list[dict[str, Any]] = []
    player_stats: list[dict[str, Any]] = []

    for item in records:
        if item.get("kind") != "match":
            continue
        try:
            raw = json.loads((run_dir / str(item["path"])).read_bytes())
            normalized = normalize_match_payload(raw)
        except (OSError, json.JSONDecodeError, APFError) as error:
            raise LoadError(f"APF run match response is invalid: {item.get('path')}") from error
        match = normalized["match"]
        source_id = str(match.get("id") or "")
        if source_id != str(item["source_entity_id"]):
            raise LoadError("APF run match ID does not match its manifest")
        number = int(item.get("gameweek") or 0)
        if number not in selected:
            raise LoadError("APF run match is outside selected rounds")
        home = (match.get("teams") or {}).get("home") or {}
        away = (match.get("teams") or {}).get("away") or {}
        if not home.get("id") or not away.get("id"):
            raise LoadError(f"APF match {source_id} has no teams")
        snapshot_id = snapshot_by_source.get(source_id)
        if snapshot_id is None:
            raise LoadError(f"APF run match {source_id} has no snapshot")
        for team in (home, away):
            team_id = str(team["id"])
            teams[team_id] = {"source": dict(team), "snapshot_id": snapshot_id}
        score = match.get("score") or {}
        matches.append({
            "source_id": source_id,
            "id": _canonical_id("match", source_id),
            "round": number,
            "status": "finished",
            "source_status": str(match.get("status") or ""),
            "period": str(match.get("period") or "FT"),
            "home_team_source_id": str(home["id"]),
            "away_team_source_id": str(away["id"]),
            "home_score": score.get("homeScore"),
            "away_score": score.get("awayScore"),
            "home_score_half_time": score.get("homeScoreHalfTime"),
            "away_score_half_time": score.get("awayScoreHalfTime"),
            "snapshot_id": snapshot_id,
        })
        for row in normalized.get("player_stats") or []:
            player, team_id = row.get("player"), str(row.get("team_id") or "")
            if not isinstance(player, Mapping) or not player.get("id") or not team_id:
                continue
            player_id = str(player["id"])
            players[player_id] = dict(player)
            membership = memberships.setdefault((player_id, team_id), {"evidence": {"player_stat": True},
                "first_seen_at": str(item.get("fetched_at") or manifest["finished_at"]),
                "last_seen_at": str(item.get("fetched_at") or manifest["finished_at"]), "snapshot_id": snapshot_id})
            # A player seen in several rounds is still one membership row, so carry
            # the latest sighting forward the way the network collector does.
            membership["last_seen_at"] = str(item.get("fetched_at") or manifest["finished_at"])
            membership["snapshot_id"] = snapshot_id
            raw_stats = dict(row.get("stats") or {})
            rating = raw_stats.get("rating")
            if rating is not None and (
                isinstance(rating, bool)
                or not isinstance(rating, (int, float))
                or not 0 <= rating <= 10
            ):
                raise LoadError(f"APF match {source_id} has an invalid player rating")
            player_stats.append({
                "source_id": f"{source_id}:{player_id}",
                "match_source_id": source_id,
                "player_source_id": player_id,
                "team_source_id": team_id,
                "canonical": dict(row.get("canonical") or {}),
                "rating": rating,
                "raw_stats": raw_stats,
                "snapshot_id": snapshot_id,
            })
    if len(matches) != len(manifest.get("match_ids") or []):
        raise LoadError("APF run did not normalize every manifest match")
    return {
        "fetched_at": str(manifest["finished_at"]),
        "competition": dict(discovery.get("competition") or {}),
        "edition": dict(discovery.get("edition") or {}),
        "gameweeks": gameweeks,
        "snapshots": snapshots,
        "teams": teams,
        "players": players,
        "memberships": memberships,
        "matches": matches,
        "player_stats": player_stats,
        "finished_matches": len(matches),
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


def _resolved_gameweek_alias_sql(
    source_id: str,
    name: str,
    number: int,
    metadata: Mapping[str, Any],
    edition_id: str,
    seen_at: str,
) -> str:
    """Alias an APF gameweek onto the row that actually holds that gameweek.

    ``gameweek`` is unique by ``(edition_id, number)`` and each provider derives
    its own id for the same week, so whichever loader ran first owns the id.
    Reading the id back out of the table keeps the alias pointing at the stored
    row instead of a recomputed id that may name nothing.
    """
    return f"""INSERT INTO source_entity
(source_code, entity_type, source_id, canonical_id, source_name, metadata, first_seen_at, last_seen_at)
SELECT 'apf', {_sql_text('gameweek')}, {_sql_text(source_id)}, existing.id, {_sql_text(name)},
       {_sql_json(metadata)}::jsonb, {_sql_text(seen_at)}, {_sql_text(seen_at)}
FROM gameweek existing
WHERE existing.edition_id = {_sql_text(edition_id)} AND existing.number = {number}
ON CONFLICT (source_code, entity_type, source_id) DO UPDATE SET
source_name = COALESCE(source_entity.source_name, EXCLUDED.source_name),
metadata = source_entity.metadata || EXCLUDED.metadata,
last_seen_at = GREATEST(source_entity.last_seen_at, EXCLUDED.last_seen_at);"""


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
    # Keep a remote load atomic and avoid one psql network round trip per row.
    statements = ["BEGIN;", "SET LOCAL lock_timeout = '15s';", "DO $apf_load$ BEGIN"]

    statements.append(
        """INSERT INTO data_source (code, display_name, base_url, is_canonical)
VALUES ('apf', 'Asociación Paraguaya de Fútbol', 'https://www.apf.org.py', true)
ON CONFLICT (code) DO UPDATE SET display_name = EXCLUDED.display_name,
base_url = EXCLUDED.base_url, is_canonical = EXCLUDED.is_canonical;"""
    )
    for snapshot in dataset["snapshots"]:
        if not snapshot.get("body_uri"):
            raise LoadError("APF snapshots must reference durable persisted body_uri files")
        statements.append(
            "INSERT INTO raw_snapshot "
            "(id, source_code, endpoint_kind, source_entity_id, source_url, fetched_at, payload_sha256, body_uri) VALUES ("
            + ", ".join(
                (
                    _sql_text(snapshot["id"]),
                    "'apf'",
                    _sql_text(snapshot["kind"]),
                    _sql_text(snapshot["source_id"]),
                    _sql_text(snapshot["url"]),
                    _sql_text(snapshot["fetched_at"]),
                    _sql_text(snapshot["digest"]),
                    _sql_text(snapshot["body_uri"]),
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
ON CONFLICT (edition_id, number) DO UPDATE SET
stage_id = EXCLUDED.stage_id, name = EXCLUDED.name, updated_at = now();"""
        )
        # A gameweek is unique by (edition, number), not by provider id, and the
        # SofaScore loader inserts its own fallback id for the same week. Upserting
        # on the id instead would violate that unique constraint, and the alias
        # below must describe the row that survived rather than a recomputed id.
        statements.append(
            _resolved_gameweek_alias_sql(
                source_id, name, number, {"week": number}, edition_id, seen_at
            )
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

    for match in dataset.get("matches", []):
        home_id = _canonical_id("team", match["home_team_source_id"])
        away_id = _canonical_id("team", match["away_team_source_id"])
        statements.append(
            f"""INSERT INTO football_match
(id, edition_id, gameweek_id, status, source_status, period, home_team_id, away_team_id,
 home_score, away_score, home_score_half_time, away_score_half_time, last_snapshot_id) VALUES
({_sql_text(match['id'])}, {_sql_text(edition_id)},
 (SELECT id FROM gameweek WHERE edition_id = {_sql_text(edition_id)} AND number = {match['round']}),
 {_sql_text(match['status'])}, {_sql_text(match['source_status'])}, {_sql_text(match['period'])},
 {_sql_text(home_id)}, {_sql_text(away_id)}, {_sql_text(match['home_score'])}, {_sql_text(match['away_score'])},
 {_sql_text(match['home_score_half_time'])}, {_sql_text(match['away_score_half_time'])}, {_sql_text(match['snapshot_id'])})
ON CONFLICT (id) DO UPDATE SET gameweek_id = EXCLUDED.gameweek_id, status = EXCLUDED.status,
source_status = EXCLUDED.source_status, period = EXCLUDED.period, home_score = EXCLUDED.home_score,
away_score = EXCLUDED.away_score, home_score_half_time = EXCLUDED.home_score_half_time,
away_score_half_time = EXCLUDED.away_score_half_time, last_snapshot_id = EXCLUDED.last_snapshot_id,
updated_at = now();"""
        )
        statements.append(_source_entity_sql("match", match["source_id"], match["id"], None,
            {"round": match["round"]}, seen_at))

    stat_columns = ["minutes", "goals", "assists", "yellow_cards", "red_cards", "rating"]
    for stat in dataset.get("player_stats", []):
        player_id = _canonical_id("player", stat["player_source_id"])
        team_id = _canonical_id("team", stat["team_source_id"])
        values = [stat["canonical"].get(column) if column != "rating" else stat["rating"] for column in stat_columns]
        stat_id = _canonical_id("player_stat", stat["source_id"])
        statements.append(
            "INSERT INTO player_match_stat\n(id, match_id, player_id, team_id, " + ", ".join(stat_columns)
            + ", raw_stats, last_snapshot_id) VALUES\n(" + ", ".join([_sql_text(stat_id),
                _sql_text(_canonical_id("match", stat["match_source_id"])), _sql_text(player_id), _sql_text(team_id),
                *(_sql_text(value) for value in values), _sql_json(stat["raw_stats"]), _sql_text(stat["snapshot_id"])])
            + ") ON CONFLICT (match_id, player_id) DO UPDATE SET "
            + ", ".join(f"{column} = EXCLUDED.{column}" for column in stat_columns)
            + ", raw_stats = EXCLUDED.raw_stats, last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"
        )
        statements.append(_source_entity_sql("player_stat", stat["source_id"], stat_id, None,
            {"match_id": stat["match_source_id"], "player_id": stat["player_source_id"], "team_id": stat["team_source_id"]}, seen_at))

    statements.extend(("END $apf_load$;", "COMMIT;"))
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
    parser.add_argument("--run-dir", type=Path, required=True, help="completed immutable APF run to validate and load (no network I/O)")
    parser.add_argument("--dry-run", action="store_true", help="collect and validate without writing")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        dataset = collect_run(args.run_dir)
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
