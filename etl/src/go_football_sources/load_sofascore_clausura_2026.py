"""Load the persisted SofaScore Clausura 2026 dataset into PostgreSQL."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import os
import re
import subprocess
import sys
from collections import Counter, defaultdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping
from uuid import UUID, uuid5


TOURNAMENT_ID = "11541"
SEASON_ID = "87613"
APF_COMPETITION_ID = "d0d19bb5-d510-4231-b2f5-74996bf0b2ef"
APF_EDITION_ID = "8b239701-bd44-4d87-8a7b-19ba464d44e5"
APF_STAGE_ID = "bf0d0c95-cb0d-47e7-8ea9-f9dfb9b11a9d"
ID_NAMESPACE = UUID("27e65a85-811b-4c82-a7ce-0e8cafbd6d15")
POSITION_CODES = {"G": "GK", "D": "DEF", "M": "MID", "F": "FWD"}
DETAIL_ENDPOINTS = {
    "basic": "",
    "statistics": "/statistics",
    "team_streaks": "/team-streaks",
    "pregame_form": "/pregame-form",
    "h2h": "/h2h",
    "lineups": "/lineups",
    "incidents": "/incidents",
}
STAT_FIELDS = {
    "minutes": "minutesPlayed",
    "goals": "goals",
    "assists": "goalAssist",
    "saves": "saves",
    "saves_inside_box": "savedShotsFromInsideTheBox",
    "passes_completed": "accuratePass",
    "passes_attempted": "totalPass",
    "shots": "totalShots",
    "shots_on_target": "onTargetScoringAttempt",
    "touches": "touches",
    "duels_won": "duelWon",
    "tackles_won": "wonTackle",
    "tackles_attempted": "totalTackle",
    "interceptions": "interceptionWon",
    "clearances": "totalClearance",
    "recoveries": "ballRecovery",
    "fouls_committed": "fouls",
    "fouls_suffered": "wasFouled",
    "dribbles_completed": "wonContest",
    "dribbles_attempted": "totalContest",
    "crosses_completed": "accurateCross",
    "crosses_attempted": "totalCross",
    "long_balls_completed": "accurateLongBalls",
    "long_balls_attempted": "totalLongBalls",
    "offsides": "totalOffside",
    "dispossessed": "dispossessed",
}


class LoadError(RuntimeError):
    """Raised when persisted source data cannot be loaded safely."""


def _canonical_id(source: str, entity_type: str, source_id: str) -> str:
    return str(uuid5(ID_NAMESPACE, f"{source}:{entity_type}:{source_id}"))


def _apf_id(entity_type: str, source_id: str) -> str:
    return _canonical_id("apf", entity_type, source_id)


def _sofascore_id(entity_type: str, source_id: str) -> str:
    return _canonical_id("sofascore", entity_type, source_id)


def _snapshot_id(endpoint: str, source_id: str, digest: str) -> str:
    return str(
        uuid5(ID_NAMESPACE, f"sofascore:snapshot:{endpoint}:{source_id}:{digest}")
    )


def _read_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise LoadError(f"Could not read valid JSON from {path}") from error


def _fetched_at(data_root: Path) -> str:
    processed = data_root / "match_details" / "processed"
    candidates: list[int] = []
    for path in processed.glob("all_matches_*.csv"):
        match = re.fullmatch(r"all_matches_(\d+)\.csv", path.name)
        if match:
            candidates.append(int(match.group(1)))
    if not candidates:
        raise LoadError("Could not derive the extraction timestamp from processed data")
    return datetime.fromtimestamp(max(candidates), timezone.utc).isoformat()


def _file_snapshot(
    path: Path,
    endpoint: str,
    source_id: str,
    source_url: str,
    fetched_at: str,
) -> tuple[dict[str, Any], Any]:
    try:
        body = path.read_bytes()
        payload = json.loads(body)
    except (OSError, json.JSONDecodeError) as error:
        raise LoadError(f"Could not snapshot {path}") from error
    digest = hashlib.sha256(body).hexdigest()
    return (
        {
            "id": _snapshot_id(endpoint, source_id, digest),
            "kind": "discovery" if endpoint == "seasons" else (
                "gameweek" if endpoint == "round" else "match"
            ),
            "source_id": source_id,
            "url": source_url,
            "fetched_at": fetched_at,
            "digest": digest,
            "payload": payload,
        },
        payload,
    )


def _load_crosswalk(path: Path) -> dict[str, Any]:
    document = _read_json(path)
    if not isinstance(document, dict):
        raise LoadError("The identity crosswalk must be a JSON object")
    competition = document.get("competition") or {}
    edition = document.get("edition") or {}
    if (
        str(competition.get("sofascore_id")) != TOURNAMENT_ID
        or str(competition.get("apf_source_id")) != APF_COMPETITION_ID
        or str(edition.get("sofascore_id")) != SEASON_ID
        or str(edition.get("apf_source_id")) != APF_EDITION_ID
    ):
        raise LoadError("The identity crosswalk is not for Clausura 2026")
    teams = document.get("teams")
    players = document.get("players")
    if not isinstance(teams, dict) or not isinstance(players, dict):
        raise LoadError("The identity crosswalk must contain team and player maps")
    if len(set(map(str, teams.values()))) != len(teams):
        raise LoadError("The team crosswalk is not one-to-one")
    if len(set(map(str, players.values()))) != len(players):
        raise LoadError("The player crosswalk is not one-to-one")
    return {
        "teams": {str(key): str(value) for key, value in teams.items()},
        "players": {str(key): str(value) for key, value in players.items()},
    }


def _validate_basic(basic: Mapping[str, Any], match_id: str) -> None:
    tournament = ((basic.get("tournament") or {}).get("uniqueTournament") or {})
    season = basic.get("season") or {}
    status = basic.get("status") or {}
    if (
        str(tournament.get("id")) != TOURNAMENT_ID
        or str(season.get("id")) != SEASON_ID
        or str(season.get("year")) != "2026"
        or str(basic.get("id")) != match_id
    ):
        raise LoadError(f"Match {match_id} is outside the approved Clausura 2026 scope")
    if status.get("type") != "finished" or int(status.get("code") or 0) != 100:
        raise LoadError(f"Match {match_id} is not finished")
    round_number = int((basic.get("roundInfo") or {}).get("round") or 0)
    if round_number not in {1, 2, 3, 4}:
        raise LoadError(f"Match {match_id} has unexpected round {round_number}")


def _score(score: Any, key: str = "current") -> int | None:
    if not isinstance(score, Mapping) or score.get(key) is None:
        return None
    return int(score[key])


def _player_profile(player: Mapping[str, Any]) -> dict[str, Any]:
    source_id = str(player.get("id") or "")
    if not source_id:
        raise LoadError("A SofaScore player did not have an id")
    country = player.get("country") or {}
    date_of_birth = None
    if player.get("dateOfBirthTimestamp") is not None:
        date_of_birth = datetime.fromtimestamp(
            int(player["dateOfBirthTimestamp"]), timezone.utc
        ).date().isoformat()
    metadata = {
        key: player[key]
        for key in (
            "slug",
            "shortName",
            "position",
            "jerseyNumber",
            "height",
            "sofascoreId",
            "gender",
            "proposedMarketValueRaw",
        )
        if player.get(key) is not None
    }
    return {
        "source_id": source_id,
        "name": str(player.get("name") or source_id),
        "given_name": player.get("firstName") or None,
        "family_name": player.get("lastName") or None,
        "date_of_birth": date_of_birth,
        "nationality_code": country.get("alpha2") or None,
        "metadata": metadata,
    }


def _team_profile(team: Mapping[str, Any]) -> dict[str, Any]:
    source_id = str(team.get("id") or "")
    if not source_id:
        raise LoadError("A SofaScore team did not have an id")
    metadata = {
        key: team[key]
        for key in ("slug", "fullName", "nameCode", "gender", "teamColors")
        if team.get(key) is not None
    }
    return {
        "source_id": source_id,
        "name": str(team.get("name") or source_id),
        "short_name": team.get("shortName") or None,
        "acronym": team.get("nameCode") or None,
        "metadata": metadata,
    }


def _stat_value(stats: Mapping[str, Any], source_key: str) -> int | None:
    value = stats.get(source_key)
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return int(value)
    return None


def _rating_value(stats: Mapping[str, Any]) -> float | None:
    """Return SofaScore's decimal match rating without coercing it to an integer."""
    value = stats.get("rating")
    if value is None:
        return None
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise LoadError(f"Invalid SofaScore player rating: {value!r}")
    rating = float(value)
    if not 0 <= rating <= 10:
        raise LoadError(f"SofaScore player rating is outside [0, 10]: {value!r}")
    return rating


def _team_stats(payload: Mapping[str, Any], side: str) -> dict[str, Any]:
    value_key = f"{side}Value"
    display_key = side
    periods: list[dict[str, Any]] = []
    for period in payload.get("statistics") or []:
        groups: list[dict[str, Any]] = []
        for group in period.get("groups") or []:
            items: list[dict[str, Any]] = []
            for item in group.get("statisticsItems") or []:
                value = {
                    key: item[key]
                    for key in (
                        "key",
                        "name",
                        "statisticsType",
                        "valueType",
                        "renderType",
                    )
                    if item.get(key) is not None
                }
                value["value"] = item.get(value_key)
                value["display"] = item.get(display_key)
                items.append(value)
            groups.append(
                {"group_name": group.get("groupName"), "statistics": items}
            )
        periods.append({"period": period.get("period"), "groups": groups})
    return {"source": "sofascore", "periods": periods}


def _incident_source_id(match_id: str, incident: Mapping[str, Any], occurrence: int) -> str:
    if incident.get("id") is not None:
        return str(incident["id"])
    encoded = json.dumps(
        incident, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    ).encode("utf-8")
    digest = hashlib.sha256(encoded).hexdigest()[:24]
    return f"{match_id}:synthetic:{digest}:{occurrence}"


def _incident_period(incident: Mapping[str, Any]) -> str | None:
    text = str(incident.get("text") or "").upper()
    if text == "HT":
        return "1H"
    if text == "FT":
        return "2H"
    minute = incident.get("time")
    if isinstance(minute, (int, float)):
        return "1H" if minute <= 45 else "2H"
    return None


def _source_url(match_id: str, endpoint: str) -> str:
    return f"https://www.sofascore.com/api/v1/event/{match_id}{DETAIL_ENDPOINTS[endpoint]}"


def _validate_csv_inventory(data_root: Path, expected_match_ids: set[str]) -> None:
    summary_files = list((data_root / "matches").glob(f"{TOURNAMENT_ID}_*/*{SEASON_ID}*_summary.csv"))
    processed_files = list((data_root / "match_details" / "processed").glob("all_matches_*.csv"))
    for path in summary_files + processed_files:
        try:
            with path.open(encoding="utf-8-sig", newline="") as handle:
                ids = {str(row.get("match_id") or "") for row in csv.DictReader(handle)}
        except OSError as error:
            raise LoadError(f"Could not validate {path}") from error
        if ids != expected_match_ids:
            raise LoadError(f"CSV inventory in {path} does not match detail files")


def collect(data_root: Path, crosswalk_path: Path) -> dict[str, Any]:
    """Validate and normalize the persisted SofaScore data without network I/O."""
    crosswalk = _load_crosswalk(crosswalk_path)
    fetched_at = _fetched_at(data_root)
    snapshots: list[dict[str, Any]] = []

    season_path = data_root / "seasons" / f"{TOURNAMENT_ID}_Paraguay_Primera_División_Clausura_seasons.json"
    season_snapshot, seasons = _file_snapshot(
        season_path,
        "seasons",
        TOURNAMENT_ID,
        f"https://www.sofascore.com/api/v1/unique-tournament/{TOURNAMENT_ID}/seasons",
        fetched_at,
    )
    snapshots.append(season_snapshot)
    if not any(str(item.get("id")) == SEASON_ID for item in seasons.get("seasons") or []):
        raise LoadError("The persisted season discovery does not include Clausura 2026")

    round_directories = list((data_root / "matches").glob(f"{TOURNAMENT_ID}_*/*{SEASON_ID}*"))
    round_directories = [path for path in round_directories if path.is_dir()]
    if len(round_directories) != 1:
        raise LoadError("Expected one Clausura 2026 round directory")
    scheduled_match_ids: set[str] = set()
    round_counts: Counter[int] = Counter()
    for round_number in range(1, 5):
        path = round_directories[0] / f"round_{round_number}.json"
        snapshot, payload = _file_snapshot(
            path,
            "round",
            f"{SEASON_ID}:round:{round_number}",
            f"https://www.sofascore.com/api/v1/unique-tournament/{TOURNAMENT_ID}/season/{SEASON_ID}/events/round/{round_number}",
            fetched_at,
        )
        snapshots.append(snapshot)
        events = payload.get("events") or []
        if int(payload.get("round") or round_number) != round_number or len(events) != 6:
            raise LoadError(f"Round {round_number} must contain six matches")
        for event in events:
            source_id = str(event.get("id") or "")
            if not source_id or source_id in scheduled_match_ids:
                raise LoadError(f"Round {round_number} contains an invalid duplicate match id")
            scheduled_match_ids.add(source_id)
            round_counts[round_number] += 1

    detail_directories = [
        path
        for path in (data_root / "match_details").glob("*/season_*")
        if path.is_dir()
        and any(
            str((_read_json(basic_path).get("season") or {}).get("id")) == SEASON_ID
            for basic_path in path.glob("*/basic.json")
        )
    ]
    if len(detail_directories) != 1:
        raise LoadError("Expected one Clausura 2026 match-detail directory")
    match_paths = sorted(
        (path for path in detail_directories[0].iterdir() if path.is_dir()),
        key=lambda path: int(path.name),
    )
    detail_match_ids = {path.name for path in match_paths}
    if len(match_paths) != 24 or detail_match_ids != scheduled_match_ids:
        raise LoadError("Expected the same 24 matches in rounds and match details")
    _validate_csv_inventory(data_root, detail_match_ids)

    matches: list[dict[str, Any]] = []
    teams: dict[str, dict[str, Any]] = {}
    players: dict[str, dict[str, Any]] = {}
    venues: dict[str, dict[str, Any]] = {}
    lineups: list[dict[str, Any]] = []
    player_stats: list[dict[str, Any]] = []
    team_stats: list[dict[str, Any]] = []
    events: list[dict[str, Any]] = []
    memberships: dict[tuple[str, str], dict[str, Any]] = {}
    position_evidence: dict[str, Counter[str]] = defaultdict(Counter)
    gameweek_times: dict[int, list[int]] = defaultdict(list)

    for match_path in match_paths:
        match_id = match_path.name
        payloads: dict[str, Any] = {}
        snapshot_ids: dict[str, str] = {}
        for endpoint in DETAIL_ENDPOINTS:
            path = match_path / f"{endpoint}.json"
            if not path.exists():
                if endpoint == "pregame_form":
                    continue
                raise LoadError(f"Match {match_id} is missing {endpoint}.json")
            source_id = match_id if endpoint == "basic" else f"{match_id}:{endpoint}"
            snapshot, payload = _file_snapshot(
                path, endpoint, source_id, _source_url(match_id, endpoint), fetched_at
            )
            snapshots.append(snapshot)
            payloads[endpoint] = payload
            snapshot_ids[endpoint] = snapshot["id"]

        basic = payloads["basic"]
        _validate_basic(basic, match_id)
        round_number = int(basic["roundInfo"]["round"])
        kickoff = int(basic["startTimestamp"])
        gameweek_times[round_number].append(kickoff)
        home = _team_profile(basic["homeTeam"])
        away = _team_profile(basic["awayTeam"])
        for team in (home, away):
            teams[team["source_id"]] = team
            team["last_snapshot_id"] = snapshot_ids["basic"]
        venue_id = None
        venue = basic.get("venue")
        if isinstance(venue, Mapping) and venue.get("id"):
            venue_source_id = str(venue["id"])
            venue_id = _sofascore_id("venue", venue_source_id)
            city = venue.get("city") or {}
            venues[venue_source_id] = {
                "id": venue_id,
                "source_id": venue_source_id,
                "name": str(venue.get("name") or venue_source_id),
                "city": city.get("name") or None,
                "metadata": {
                    key: venue[key]
                    for key in ("slug", "capacity", "venueCoordinates", "stadium")
                    if venue.get(key) is not None
                },
            }
        matches.append(
            {
                "source_id": match_id,
                "id": _sofascore_id("match", match_id),
                "round": round_number,
                "kickoff_at": datetime.fromtimestamp(kickoff, timezone.utc).isoformat(),
                "status": "finished",
                "source_status": str((basic.get("status") or {}).get("description") or ""),
                "period": "FT",
                "home_team_source_id": home["source_id"],
                "away_team_source_id": away["source_id"],
                "home_score": _score(basic.get("homeScore")),
                "away_score": _score(basic.get("awayScore")),
                "home_score_half_time": _score(basic.get("homeScore"), "period1"),
                "away_score_half_time": _score(basic.get("awayScore"), "period1"),
                "home_score_extra_time": _score(basic.get("homeScore"), "period3"),
                "away_score_extra_time": _score(basic.get("awayScore"), "period3"),
                "home_score_penalty": _score(basic.get("homeScore"), "penalties"),
                "away_score_penalty": _score(basic.get("awayScore"), "penalties"),
                "venue_id": venue_id,
                "snapshot_id": snapshot_ids["basic"],
                "metadata": {"slug": basic.get("slug"), "round": round_number},
            }
        )

        lineup_payload = payloads["lineups"]
        if lineup_payload.get("confirmed") is not True:
            raise LoadError(f"Match {match_id} does not have confirmed lineups")
        seen_match_players: set[str] = set()
        for side, team in (("home", home), ("away", away)):
            side_payload = lineup_payload.get(side) or {}
            for row in side_payload.get("players") or []:
                profile = _player_profile(row.get("player") or {})
                player_source_id = profile["source_id"]
                if player_source_id in seen_match_players:
                    raise LoadError(f"Player {player_source_id} is duplicated in match {match_id}")
                seen_match_players.add(player_source_id)
                reported_team_source_id = (
                    str(row.get("teamId")) if row.get("teamId") is not None else None
                )
                players[player_source_id] = profile
                profile["last_snapshot_id"] = snapshot_ids["lineups"]
                position = POSITION_CODES.get(str(row.get("position") or ""))
                if position is None:
                    raise LoadError(f"Player {player_source_id} has an unknown position")
                raw_stats = row.get("statistics") or {}
                minutes = _stat_value(raw_stats, "minutesPlayed") or 0
                position_evidence[player_source_id][position] += max(minutes, 1)
                lineup_source_id = f"{match_id}:{player_source_id}"
                lineups.append(
                    {
                        "id": _sofascore_id("lineup", lineup_source_id),
                        "source_id": lineup_source_id,
                        "match_id": _sofascore_id("match", match_id),
                        "match_source_id": match_id,
                        "player_source_id": player_source_id,
                        "team_source_id": team["source_id"],
                        "reported_team_source_id": reported_team_source_id,
                        "squad_role": "substitute" if row.get("substitute") else "starter",
                        "captain": row.get("captain") if "captain" in row else None,
                        "shirt_number": row.get("shirtNumber"),
                        "formation_place": None,
                        "position_code": position,
                        "snapshot_id": snapshot_ids["lineups"],
                        "formation": side_payload.get("formation"),
                    }
                )
                stat_source_id = f"{match_id}:{player_source_id}"
                canonical = {
                    field: _stat_value(raw_stats, source_key)
                    for field, source_key in STAT_FIELDS.items()
                }
                canonical.update(
                    {
                        "rating": _rating_value(raw_stats),
                        "yellow_cards": None,
                        "red_cards": None,
                    }
                )
                player_stats.append(
                    {
                        "id": _sofascore_id("player_stat", stat_source_id),
                        "source_id": stat_source_id,
                        "match_id": _sofascore_id("match", match_id),
                        "match_source_id": match_id,
                        "player_source_id": player_source_id,
                        "team_source_id": team["source_id"],
                        "canonical": canonical,
                        "raw_stats": dict(raw_stats),
                        "snapshot_id": snapshot_ids["lineups"],
                    }
                )
                membership = memberships.setdefault(
                    (player_source_id, team["source_id"]),
                    {
                        "first_seen_at": fetched_at,
                        "last_seen_at": fetched_at,
                        "snapshot_id": snapshot_ids["lineups"],
                        "matches": 0,
                    },
                )
                membership["matches"] += 1
                membership["snapshot_id"] = snapshot_ids["lineups"]

        for side, team in (("home", home), ("away", away)):
            stat_source_id = f"{match_id}:{team['source_id']}"
            team_stats.append(
                {
                    "id": _sofascore_id("team_stat", stat_source_id),
                    "source_id": stat_source_id,
                    "match_id": _sofascore_id("match", match_id),
                    "match_source_id": match_id,
                    "team_source_id": team["source_id"],
                    "raw_stats": _team_stats(payloads["statistics"], side),
                    "snapshot_id": snapshot_ids["statistics"],
                }
            )

        synthetic_occurrences: Counter[str] = Counter()
        incidents = list(payloads["incidents"].get("incidents") or [])
        for event_order, incident in enumerate(reversed(incidents)):
            encoded = json.dumps(
                incident, ensure_ascii=False, sort_keys=True, separators=(",", ":")
            )
            fingerprint = hashlib.sha256(encoded.encode("utf-8")).hexdigest()[:24]
            occurrence = synthetic_occurrences[fingerprint]
            synthetic_occurrences[fingerprint] += 1
            event_source_id = _incident_source_id(match_id, incident, occurrence)
            player = incident.get("player")
            related_player = incident.get("assist1")
            if incident.get("incidentType") == "substitution":
                player = incident.get("playerOut")
                related_player = incident.get("playerIn")
            player_source_id = str(player.get("id")) if isinstance(player, Mapping) and player.get("id") else None
            if incident.get("incidentType") == "card" and player_source_id:
                for stat in reversed(player_stats):
                    if (
                        stat["match_source_id"] == match_id
                        and stat["player_source_id"] == player_source_id
                    ):
                        card_class = incident.get("incidentClass")
                        card_field = (
                            "yellow_cards" if card_class == "yellow" else (
                                "red_cards" if card_class in {"red", "yellowRed"} else None
                            )
                        )
                        if card_field:
                            stat["canonical"][card_field] = (
                                stat["canonical"][card_field] or 0
                            ) + 1
                        break
            related_source_id = (
                str(related_player.get("id"))
                if isinstance(related_player, Mapping) and related_player.get("id")
                else None
            )
            is_home = incident.get("isHome")
            team_source_id = home["source_id"] if is_home is True else (
                away["source_id"] if is_home is False else None
            )
            events.append(
                {
                    "id": _sofascore_id("event", event_source_id),
                    "source_id": event_source_id,
                    "match_id": _sofascore_id("match", match_id),
                    "match_source_id": match_id,
                    "event_order": event_order,
                    "event_type": str(incident.get("incidentType") or "unknown"),
                    "qualification_type": incident.get("incidentClass") or None,
                    "team_source_id": team_source_id,
                    "player_source_id": player_source_id,
                    "related_player_source_id": related_source_id,
                    "period": _incident_period(incident),
                    "minute": (
                        int(incident["time"])
                        if incident.get("time") is not None and int(incident["time"]) >= 0
                        else (
                            int(incident["benchTime"])
                            if incident.get("benchTime") is not None
                            else None
                        )
                    ),
                    "home_score": incident.get("homeScore"),
                    "away_score": incident.get("awayScore"),
                    "qualifiers": dict(incident),
                    "is_active": not bool(incident.get("rescinded")),
                    "snapshot_id": snapshot_ids["incidents"],
                }
            )

    if set(teams) != set(crosswalk["teams"]):
        raise LoadError("Observed SofaScore teams do not exactly match the reviewed crosswalk")
    if set(players) != set(crosswalk["players"]):
        missing = sorted(set(players) - set(crosswalk["players"]))
        raise LoadError(f"Observed players do not exactly match the reviewed crosswalk: {missing}")
    if len(lineups) != 1102 or len(player_stats) != 1102:
        raise LoadError("Expected 1,102 lineup and player-stat rows")
    if len(team_stats) != 48:
        raise LoadError("Expected 48 team-stat rows")

    profiles = {
        source_id: max(counter, key=lambda value: (counter[value], value))
        for source_id, counter in position_evidence.items()
    }
    return {
        "fetched_at": fetched_at,
        "crosswalk": crosswalk,
        "snapshots": snapshots,
        "matches": matches,
        "teams": teams,
        "players": players,
        "venues": venues,
        "lineups": lineups,
        "player_stats": player_stats,
        "team_stats": team_stats,
        "events": events,
        "memberships": memberships,
        "profiles": profiles,
        "gameweek_times": gameweek_times,
        "round_counts": round_counts,
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
            "'sofascore'",
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


def _canonical_team(dataset: Mapping[str, Any], source_id: str | None) -> str | None:
    if source_id is None:
        return None
    return _apf_id("team", dataset["crosswalk"]["teams"][source_id])


def _canonical_player(dataset: Mapping[str, Any], source_id: str | None) -> str | None:
    if source_id is None:
        return None
    try:
        apf_source_id = dataset["crosswalk"]["players"][source_id]
    except KeyError as error:
        raise LoadError(f"Player {source_id} is absent from the reviewed crosswalk") from error
    return _apf_id("player", apf_source_id)


def build_sql(dataset: Mapping[str, Any]) -> str:
    """Build one transactional and idempotent PostgreSQL load."""
    seen_at = str(dataset["fetched_at"])
    competition_id = _apf_id("competition", APF_COMPETITION_ID)
    edition_id = _apf_id("edition", APF_EDITION_ID)
    stage_id = _apf_id("stage", APF_STAGE_ID)
    # psql normally sends each statement as a separate network round trip. The
    # DO block keeps this large remote load atomic while requiring one round trip.
    statements = [
        "BEGIN;",
        "SET LOCAL lock_timeout = '15s';",
        "DO $sofascore_load$ BEGIN",
    ]
    statements.append(
        """INSERT INTO data_source (code, display_name, base_url, is_canonical)
VALUES ('sofascore', 'SofaScore', 'https://www.sofascore.com', false)
ON CONFLICT (code) DO UPDATE SET display_name = EXCLUDED.display_name,
base_url = EXCLUDED.base_url, is_canonical = false;"""
    )
    for snapshot in dataset["snapshots"]:
        statements.append(
            "INSERT INTO raw_snapshot "
            "(id, source_code, endpoint_kind, source_entity_id, source_url, fetched_at, payload_sha256, payload) VALUES ("
            + ", ".join(
                (
                    _sql_text(snapshot["id"]),
                    "'sofascore'",
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

    statements.extend(
        (
            f"""INSERT INTO competition (id, name, country_code) VALUES
({_sql_text(competition_id)}, 'Paraguay Primera División (Clausura)', 'PY')
ON CONFLICT (id) DO UPDATE SET updated_at = now();""",
            f"""INSERT INTO edition (id, competition_id, name, season_year, starts_on) VALUES
({_sql_text(edition_id)}, {_sql_text(competition_id)}, 'Clausura 2026', 2026, '2026-07-24')
ON CONFLICT (id) DO UPDATE SET season_year = 2026,
starts_on = COALESCE(edition.starts_on, EXCLUDED.starts_on), updated_at = now();""",
            f"""INSERT INTO tournament_stage (id, edition_id, name, stage_order) VALUES
({_sql_text(stage_id)}, {_sql_text(edition_id)}, 'Regular', 1)
ON CONFLICT (id) DO UPDATE SET updated_at = now();""",
            _source_entity_sql(
                "competition", TOURNAMENT_ID, competition_id,
                "Primera División, Clausura", {}, seen_at
            ),
            _source_entity_sql(
                "edition", SEASON_ID, edition_id,
                "Division de Honor, Clausura 2026", {"season_year": 2026}, seen_at
            ),
            _source_entity_sql(
                "stage", f"{SEASON_ID}:regular", stage_id, "Regular", {}, seen_at
            ),
        )
    )
    for number in range(1, 5):
        source_id = f"{SEASON_ID}:round:{number}"
        fallback_id = _sofascore_id("gameweek", source_id)
        timestamps = dataset["gameweek_times"][number]
        starts_at = datetime.fromtimestamp(min(timestamps), timezone.utc).isoformat()
        ends_at = datetime.fromtimestamp(max(timestamps), timezone.utc).isoformat()
        statements.append(
            f"""INSERT INTO gameweek (id, edition_id, stage_id, number, name, starts_at, ends_at) VALUES
({_sql_text(fallback_id)}, {_sql_text(edition_id)}, {_sql_text(stage_id)}, {number}, 'Jornada {number}',
 {_sql_text(starts_at)}, {_sql_text(ends_at)})
ON CONFLICT (edition_id, number) DO UPDATE SET
starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, updated_at = now();"""
        )
        statements.append(
            f"""INSERT INTO source_entity
(source_code, entity_type, source_id, canonical_id, source_name, metadata, first_seen_at, last_seen_at)
SELECT 'sofascore', 'gameweek', {_sql_text(source_id)}, id, 'Jornada {number}',
       {_sql_json({'round': number})}, {_sql_text(seen_at)}, {_sql_text(seen_at)}
FROM gameweek WHERE edition_id = {_sql_text(edition_id)} AND number = {number}
ON CONFLICT (source_code, entity_type, source_id) DO UPDATE SET
last_seen_at = GREATEST(source_entity.last_seen_at, EXCLUDED.last_seen_at);"""
        )

    for source_id, team in dataset["teams"].items():
        canonical_id = _canonical_team(dataset, source_id)
        statements.append(
            f"""INSERT INTO team (id, name, short_name, acronym) VALUES
({_sql_text(canonical_id)}, {_sql_text(team['name'])}, {_sql_text(team['short_name'])}, {_sql_text(team['acronym'])})
ON CONFLICT (id) DO UPDATE SET
short_name = COALESCE(team.short_name, EXCLUDED.short_name),
acronym = COALESCE(team.acronym, EXCLUDED.acronym), updated_at = now();"""
        )
        statements.append(
            _source_entity_sql(
                "team", source_id, str(canonical_id), team["name"], team["metadata"], seen_at
            )
        )
        statements.append(
            f"""INSERT INTO edition_team (edition_id, team_id, last_snapshot_id) VALUES
({_sql_text(edition_id)}, {_sql_text(canonical_id)}, {_sql_text(team['last_snapshot_id'])})
ON CONFLICT (edition_id, team_id) DO UPDATE SET
last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"""
        )

    for source_id, player in dataset["players"].items():
        canonical_id = _canonical_player(dataset, source_id)
        statements.append(
            f"""INSERT INTO player
(id, display_name, given_name, family_name, date_of_birth, nationality_code) VALUES
({_sql_text(canonical_id)}, {_sql_text(player['name'])}, {_sql_text(player['given_name'])},
 {_sql_text(player['family_name'])}, {_sql_text(player['date_of_birth'])}, {_sql_text(player['nationality_code'])})
ON CONFLICT (id) DO UPDATE SET
given_name = COALESCE(NULLIF(EXCLUDED.given_name, ''), player.given_name),
family_name = COALESCE(NULLIF(EXCLUDED.family_name, ''), player.family_name),
date_of_birth = COALESCE(EXCLUDED.date_of_birth, player.date_of_birth),
nationality_code = COALESCE(EXCLUDED.nationality_code, player.nationality_code),
updated_at = now();"""
        )
        metadata = dict(player["metadata"])
        metadata["apf_source_id"] = dataset["crosswalk"]["players"][source_id]
        statements.append(
            _source_entity_sql(
                "player", source_id, str(canonical_id), player["name"], metadata, seen_at
            )
        )
        statements.append(
            f"""INSERT INTO player_edition_profile
(edition_id, player_id, primary_position, resolution_method, resolution_note, source_snapshot_id) VALUES
({_sql_text(edition_id)}, {_sql_text(canonical_id)}, {_sql_text(dataset['profiles'][source_id])},
 'lineup_minutes', 'Resolved from SofaScore positions weighted by observed match minutes',
 {_sql_text(player['last_snapshot_id'])})
ON CONFLICT (edition_id, player_id) DO UPDATE SET
primary_position = EXCLUDED.primary_position,
resolution_method = EXCLUDED.resolution_method,
resolution_note = EXCLUDED.resolution_note,
source_snapshot_id = EXCLUDED.source_snapshot_id, updated_at = now();"""
        )

    for source_id, venue in dataset["venues"].items():
        statements.append(
            f"""INSERT INTO venue (id, name, city, timezone_name) VALUES
({_sql_text(venue['id'])}, {_sql_text(venue['name'])}, {_sql_text(venue['city'])}, 'America/Asuncion')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, city = EXCLUDED.city, updated_at = now();"""
        )
        statements.append(
            _source_entity_sql(
                "venue", source_id, venue["id"], venue["name"], venue["metadata"], seen_at
            )
        )

    for match in dataset["matches"]:
        home_id = _canonical_team(dataset, match["home_team_source_id"])
        away_id = _canonical_team(dataset, match["away_team_source_id"])
        statements.append(
            f"""INSERT INTO football_match
(id, edition_id, gameweek_id, venue_id, kickoff_at, status, source_status, period,
 home_team_id, away_team_id, home_score, away_score, home_score_half_time,
 away_score_half_time, home_score_extra_time, away_score_extra_time,
 home_score_penalty, away_score_penalty, last_snapshot_id) VALUES
({_sql_text(match['id'])}, {_sql_text(edition_id)},
 (SELECT id FROM gameweek WHERE edition_id = {_sql_text(edition_id)} AND number = {match['round']}),
 {_sql_text(match['venue_id'])}, {_sql_text(match['kickoff_at'])}, {_sql_text(match['status'])},
 {_sql_text(match['source_status'])}, {_sql_text(match['period'])}, {_sql_text(home_id)}, {_sql_text(away_id)},
 {_sql_text(match['home_score'])}, {_sql_text(match['away_score'])}, {_sql_text(match['home_score_half_time'])},
 {_sql_text(match['away_score_half_time'])}, {_sql_text(match['home_score_extra_time'])},
 {_sql_text(match['away_score_extra_time'])}, {_sql_text(match['home_score_penalty'])},
 {_sql_text(match['away_score_penalty'])}, {_sql_text(match['snapshot_id'])})
ON CONFLICT (id) DO UPDATE SET
gameweek_id = EXCLUDED.gameweek_id, venue_id = EXCLUDED.venue_id,
kickoff_at = EXCLUDED.kickoff_at, status = EXCLUDED.status,
source_status = EXCLUDED.source_status, period = EXCLUDED.period,
home_score = EXCLUDED.home_score, away_score = EXCLUDED.away_score,
home_score_half_time = EXCLUDED.home_score_half_time,
away_score_half_time = EXCLUDED.away_score_half_time,
home_score_extra_time = EXCLUDED.home_score_extra_time,
away_score_extra_time = EXCLUDED.away_score_extra_time,
home_score_penalty = EXCLUDED.home_score_penalty,
away_score_penalty = EXCLUDED.away_score_penalty,
last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"""
        )
        statements.append(
            _source_entity_sql(
                "match", match["source_id"], match["id"], None, match["metadata"], seen_at
            )
        )

    for (player_source_id, team_source_id), membership in dataset["memberships"].items():
        player_id = _canonical_player(dataset, player_source_id)
        team_id = _canonical_team(dataset, team_source_id)
        evidence = {"sofascore_lineup": True, "matches_observed": membership["matches"]}
        statements.append(
            f"""INSERT INTO player_edition_team
(edition_id, player_id, team_id, evidence, first_seen_at, last_seen_at, last_snapshot_id) VALUES
({_sql_text(edition_id)}, {_sql_text(player_id)}, {_sql_text(team_id)}, {_sql_json(evidence)},
 {_sql_text(membership['first_seen_at'])}, {_sql_text(membership['last_seen_at'])},
 {_sql_text(membership['snapshot_id'])})
ON CONFLICT (edition_id, player_id, team_id) DO UPDATE SET
evidence = player_edition_team.evidence || EXCLUDED.evidence,
first_seen_at = LEAST(player_edition_team.first_seen_at, EXCLUDED.first_seen_at),
last_seen_at = GREATEST(player_edition_team.last_seen_at, EXCLUDED.last_seen_at),
last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"""
        )

    for lineup in dataset["lineups"]:
        player_id = _canonical_player(dataset, lineup["player_source_id"])
        team_id = _canonical_team(dataset, lineup["team_source_id"])
        statements.append(
            f"""INSERT INTO match_lineup
(id, match_id, player_id, team_id, squad_role, captain, shirt_number,
 formation_place, position_code, last_snapshot_id) VALUES
({_sql_text(lineup['id'])}, {_sql_text(lineup['match_id'])}, {_sql_text(player_id)}, {_sql_text(team_id)},
 {_sql_text(lineup['squad_role'])}, {_sql_text(lineup['captain'])}, {_sql_text(lineup['shirt_number'])},
 {_sql_text(lineup['formation_place'])}, {_sql_text(lineup['position_code'])}, {_sql_text(lineup['snapshot_id'])})
ON CONFLICT (match_id, player_id) DO UPDATE SET
team_id = EXCLUDED.team_id, squad_role = EXCLUDED.squad_role, captain = EXCLUDED.captain,
shirt_number = EXCLUDED.shirt_number, formation_place = EXCLUDED.formation_place,
position_code = EXCLUDED.position_code, last_snapshot_id = EXCLUDED.last_snapshot_id,
updated_at = now();"""
        )
        statements.append(
            _source_entity_sql(
                "lineup", lineup["source_id"], lineup["id"], None,
                {
                    "match_id": lineup["match_source_id"],
                    "player_id": lineup["player_source_id"],
                    "team_id": lineup["team_source_id"],
                    "reported_team_id": lineup["reported_team_source_id"],
                    "formation": lineup["formation"],
                },
                seen_at,
            )
        )

    stat_columns = [
        "minutes",
        "goals",
        "assists",
        "yellow_cards",
        "red_cards",
        "rating",
        *list(STAT_FIELDS)[3:],
    ]
    for stat in dataset["player_stats"]:
        player_id = _canonical_player(dataset, stat["player_source_id"])
        team_id = _canonical_team(dataset, stat["team_source_id"])
        values = [stat["canonical"][column] for column in stat_columns]
        statements.append(
            "INSERT INTO player_match_stat\n"
            + "(id, match_id, player_id, team_id, "
            + ", ".join(stat_columns)
            + ", raw_stats, last_snapshot_id) VALUES\n("
            + ", ".join(
                [
                    _sql_text(stat["id"]),
                    _sql_text(stat["match_id"]),
                    _sql_text(player_id),
                    _sql_text(team_id),
                    *(_sql_text(value) for value in values),
                    _sql_json(stat["raw_stats"]),
                    _sql_text(stat["snapshot_id"]),
                ]
            )
            + ") ON CONFLICT (match_id, player_id) DO UPDATE SET\n"
            + ",\n".join(
                f"{column} = EXCLUDED.{column}" for column in stat_columns
            )
            + ",\nraw_stats = EXCLUDED.raw_stats, last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"
        )
        statements.append(
            _source_entity_sql(
                "player_stat", stat["source_id"], stat["id"], None,
                {
                    "match_id": stat["match_source_id"],
                    "player_id": stat["player_source_id"],
                    "team_id": stat["team_source_id"],
                },
                seen_at,
            )
        )

    for stat in dataset["team_stats"]:
        team_id = _canonical_team(dataset, stat["team_source_id"])
        statements.append(
            f"""INSERT INTO team_match_stat
(id, match_id, team_id, raw_stats, last_snapshot_id) VALUES
({_sql_text(stat['id'])}, {_sql_text(stat['match_id'])}, {_sql_text(team_id)},
 {_sql_json(stat['raw_stats'])}, {_sql_text(stat['snapshot_id'])})
ON CONFLICT (match_id, team_id) DO UPDATE SET
raw_stats = EXCLUDED.raw_stats, last_snapshot_id = EXCLUDED.last_snapshot_id, updated_at = now();"""
        )
        statements.append(
            _source_entity_sql(
                "team_stat", stat["source_id"], stat["id"], None,
                {"match_id": stat["match_source_id"], "team_id": stat["team_source_id"]},
                seen_at,
            )
        )

    for event in dataset["events"]:
        team_id = _canonical_team(dataset, event["team_source_id"])
        player_id = _canonical_player(dataset, event["player_source_id"])
        related_player_id = _canonical_player(dataset, event["related_player_source_id"])
        statements.append(
            f"""INSERT INTO match_event
(id, match_id, event_order, event_type, qualification_type, team_id, player_id,
 related_player_id, period, minute, home_score, away_score, qualifiers, is_active,
 last_snapshot_id) VALUES
({_sql_text(event['id'])}, {_sql_text(event['match_id'])}, {event['event_order']},
 {_sql_text(event['event_type'])}, {_sql_text(event['qualification_type'])}, {_sql_text(team_id)},
 {_sql_text(player_id)}, {_sql_text(related_player_id)}, {_sql_text(event['period'])},
 {_sql_text(event['minute'])}, {_sql_text(event['home_score'])}, {_sql_text(event['away_score'])},
 {_sql_json(event['qualifiers'])}, {_sql_text(event['is_active'])}, {_sql_text(event['snapshot_id'])})
ON CONFLICT (id) DO UPDATE SET
event_order = EXCLUDED.event_order, event_type = EXCLUDED.event_type,
qualification_type = EXCLUDED.qualification_type, team_id = EXCLUDED.team_id,
player_id = EXCLUDED.player_id, related_player_id = EXCLUDED.related_player_id,
period = EXCLUDED.period, minute = EXCLUDED.minute, home_score = EXCLUDED.home_score,
away_score = EXCLUDED.away_score, qualifiers = EXCLUDED.qualifiers,
is_active = EXCLUDED.is_active, last_snapshot_id = EXCLUDED.last_snapshot_id,
updated_at = now();"""
        )
        statements.append(
            _source_entity_sql(
                "event", event["source_id"], event["id"], None,
                {"match_id": event["match_source_id"]}, seen_at
            )
        )

    statements.extend(("END $sofascore_load$;", "COMMIT;"))
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
    """Execute the generated transaction through psql."""
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
        raise LoadError(result.stderr.strip() or "psql failed")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--data-root", type=Path, default=Path("tests/sofascore_scraper/data")
    )
    parser.add_argument(
        "--crosswalk",
        type=Path,
        default=Path("data/sources/sofascore-apf-clausura-2026-crosswalk.json"),
    )
    parser.add_argument("--env-file", type=Path, default=Path(".env"))
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--sql-output", type=Path)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        dataset = collect(args.data_root, args.crosswalk)
        sql = build_sql(dataset)
        if args.sql_output:
            args.sql_output.parent.mkdir(parents=True, exist_ok=True)
            args.sql_output.write_text(sql, encoding="utf-8")
        if not args.dry_run:
            load(_database_url(args.env_file), sql)
        summary = {
            "source": "sofascore",
            "edition": "Clausura 2026",
            "raw_snapshots": len(dataset["snapshots"]),
            "matches": len(dataset["matches"]),
            "teams": len(dataset["teams"]),
            "players": len(dataset["players"]),
            "lineups": len(dataset["lineups"]),
            "player_stats": len(dataset["player_stats"]),
            "player_ratings": sum(
                stat["canonical"]["rating"] is not None
                for stat in dataset["player_stats"]
            ),
            "team_stats": len(dataset["team_stats"]),
            "events": len(dataset["events"]),
            "database_loaded": not args.dry_run,
        }
        print(json.dumps(summary, ensure_ascii=False))
        return 0
    except (LoadError, OSError, ValueError, KeyError, TypeError) as error:
        print(f"error: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
