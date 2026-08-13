"""Extractor for the APF's public, Opta-backed website payloads.

The APF site is a Next.js application. Its fixture page publishes discovery data
in ``__NEXT_DATA__`` and its frontend uses public JSON routes for round and match
data. This module deliberately has no third-party dependencies.
"""

from __future__ import annotations

import hashlib
import json
import time
from datetime import datetime, timezone
from html.parser import HTMLParser
from typing import Any, Callable, Iterable, Mapping
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


APF_BASE_URL = "https://www.apf.org.py"
FIXTURES_PATH = "/partidos-clausura"
ROUND_API_PATH = "/api/football_list/football_competition_match/variant_a"
MATCH_API_PATH = "/api/live/layout/football_match"

CANONICAL_STAT_FIELDS = {
    "minutes": "minsPlayed",
    "goals": "goals",
    "assists": "goalAssist",
    "yellow_cards": "yellowCard",
    "red_cards": "redCard",
    "saves": "saves",
    "saves_inside_box": "savedIbox",
    "passes_completed": "accuratePass",
    "passes_attempted": "totalPass",
    "shots": "totalScoringAtt",
    "shots_on_target": "ontargetScoringAtt",
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


class APFError(RuntimeError):
    """Raised when the APF source cannot be fetched or decoded."""


class _NextDataParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self._capturing = False
        self._chunks: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        attributes = dict(attrs)
        if tag == "script" and attributes.get("id") == "__NEXT_DATA__":
            self._capturing = True

    def handle_endtag(self, tag: str) -> None:
        if tag == "script" and self._capturing:
            self._capturing = False

    def handle_data(self, data: str) -> None:
        if self._capturing:
            self._chunks.append(data)

    @property
    def value(self) -> str:
        return "".join(self._chunks)


def parse_next_data(html: str) -> dict[str, Any]:
    """Extract the Next.js bootstrap JSON from an HTML document."""
    parser = _NextDataParser()
    parser.feed(html)
    if not parser.value:
        raise APFError("APF page did not contain a __NEXT_DATA__ payload")
    try:
        value = json.loads(parser.value)
    except json.JSONDecodeError as error:
        raise APFError("APF __NEXT_DATA__ payload was not valid JSON") from error
    if not isinstance(value, dict):
        raise APFError("APF __NEXT_DATA__ payload had an unexpected shape")
    return value


def _walk(value: Any) -> Iterable[Any]:
    yield value
    if isinstance(value, Mapping):
        for child in value.values():
            yield from _walk(child)
    elif isinstance(value, list):
        for child in value:
            yield from _walk(child)


def _find_mapping(value: Any, predicate: Callable[[Mapping[str, Any]], bool]) -> Mapping[str, Any]:
    for candidate in _walk(value):
        if isinstance(candidate, Mapping) and predicate(candidate):
            return candidate
    raise APFError("Expected APF data module was not found")


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _identifier(value: Any) -> Any:
    if isinstance(value, Mapping):
        return value.get("id")
    return value


def _entity(value: Any) -> dict[str, Any] | None:
    if not isinstance(value, Mapping):
        return None
    wanted = ("id", "name", "shortName", "nickname", "slug", "acronym", "week", "type")
    result = {key: value[key] for key in wanted if value.get(key) is not None}
    return result or None


def _side_team_id(game: Mapping[str, Any], side: str) -> Any:
    for key in (f"{side}Team", f"{side}TeamId"):
        if key in game:
            return _identifier(game[key])
    return None


def canonical_player_stats(stats: Mapping[str, Any]) -> dict[str, Any]:
    """Map selected Opta field names to stable names while retaining raw stats."""
    return {
        canonical: stats[source]
        for canonical, source in CANONICAL_STAT_FIELDS.items()
        if source in stats and stats[source] is not None
    }


def payload_digest(value: Any) -> str:
    encoded = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(encoded.encode("utf-8")).hexdigest()


class APFClient:
    """Read APF discovery, round, and match data."""

    def __init__(
        self,
        *,
        base_url: str = APF_BASE_URL,
        timeout: float = 30.0,
        retries: int = 3,
        delay: float = 0.25,
        user_agent: str = "go-football-source-research/0.1 (+contact repository owner)",
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.retries = retries
        self.delay = delay
        self.user_agent = user_agent
        self._last_request_at = 0.0

    def _throttle(self) -> None:
        remaining = self.delay - (time.monotonic() - self._last_request_at)
        if remaining > 0:
            time.sleep(remaining)

    def _get(self, path: str, params: Mapping[str, Any] | None = None) -> tuple[bytes, str]:
        url = f"{self.base_url}{path}"
        if params:
            url = f"{url}?{urlencode(params)}"
        last_error: Exception | None = None
        for attempt in range(self.retries):
            self._throttle()
            request = Request(
                url,
                headers={"Accept": "application/json,text/html;q=0.9", "User-Agent": self.user_agent},
            )
            try:
                with urlopen(request, timeout=self.timeout) as response:
                    body = response.read()
                self._last_request_at = time.monotonic()
                return body, url
            except HTTPError as error:
                self._last_request_at = time.monotonic()
                last_error = error
                # APF's CDN occasionally emits a spurious 304 even though this
                # client sends no conditional headers; retry it like a transient.
                if error.code not in {304, 429, 500, 502, 503, 504}:
                    break
                retry_after = error.headers.get("Retry-After")
                wait = float(retry_after) if retry_after and retry_after.isdigit() else 2**attempt
                time.sleep(wait)
            except URLError as error:
                self._last_request_at = time.monotonic()
                last_error = error
                time.sleep(2**attempt)
        raise APFError(f"Could not fetch {url}: {last_error}") from last_error

    def _get_json(self, path: str, params: Mapping[str, Any] | None = None) -> tuple[Any, str]:
        body, url = self._get(path, params)
        try:
            return json.loads(body), url
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise APFError(f"APF returned invalid JSON from {url}") from error

    def discover(self) -> dict[str, Any]:
        """Discover current edition, round, gameweeks, and the selected round."""
        body, url = self._get(FIXTURES_PATH)
        try:
            bootstrap = parse_next_data(body.decode("utf-8"))
        except UnicodeDecodeError as error:
            raise APFError(f"APF returned invalid HTML from {url}") from error
        module = _find_mapping(
            bootstrap,
            lambda item: item.get("type") == "football_list"
            and item.get("subtype") == "football_competition_match",
        )
        data = module.get("data")
        if not isinstance(data, Mapping):
            raise APFError("APF fixture module did not contain a data object")

        gameweeks = data.get("gameweeks") or []
        rounds = data.get("rounds") or []
        matches = data.get("matches") or []
        if not isinstance(gameweeks, list) or not isinstance(rounds, list):
            raise APFError("APF discovery returned malformed rounds or gameweeks")

        edition_id = data.get("editionId") or _identifier(data.get("edition"))
        round_id = data.get("roundId") or _identifier(data.get("round"))
        if not edition_id or not round_id:
            raise APFError("APF discovery did not expose editionId and roundId")

        first_match = matches[0] if matches and isinstance(matches[0], Mapping) else {}
        current = data.get("currentGameweek") or data.get("current")
        if current is None:
            for candidate in rounds:
                if isinstance(candidate, Mapping) and candidate.get("currentGameweek"):
                    current = candidate["currentGameweek"]
                    break
        if current is None:
            current = first_match.get("gameweek")
        result = {
            "source": "apf",
            "source_url": url,
            "fetched_at": _utc_now(),
            "edition_id": edition_id,
            "round_id": round_id,
            "competition": _entity(data.get("competition") or first_match.get("competition")),
            "edition": _entity(data.get("edition") or first_match.get("edition")),
            "rounds": rounds,
            "gameweeks": gameweeks,
            "current_gameweek": current,
            "current_matches": matches,
        }
        result["payload_sha256"] = payload_digest(
            {key: value for key, value in result.items() if key not in {"fetched_at", "payload_sha256"}}
        )
        return result

    def get_round(self, edition_id: str, round_id: str, gameweek_id: str) -> dict[str, Any]:
        payload, url = self._get_json(
            ROUND_API_PATH,
            {"editionId": edition_id, "roundId": round_id, "gameweekId": gameweek_id},
        )
        if not isinstance(payload, Mapping):
            raise APFError("APF round endpoint returned an unexpected payload")
        result = dict(payload)
        result["source_url"] = url
        result["fetched_at"] = _utc_now()
        result["payload_sha256"] = payload_digest(payload)
        return result

    def get_match_with_raw(self, slug: str) -> tuple[dict[str, Any], dict[str, Any]]:
        """Fetch a normalized match and its unmodified source payload."""
        payload, url = self._get_json(MATCH_API_PATH, {"slug": slug})
        if not isinstance(payload, Mapping):
            raise APFError("APF match endpoint returned an unexpected payload")
        raw = dict(payload)
        normalized = normalize_match_payload(raw)
        normalized["source_url"] = url
        normalized["fetched_at"] = _utc_now()
        normalized["payload_sha256"] = payload_digest(raw)
        return normalized, raw

    def get_match(self, slug: str) -> dict[str, Any]:
        """Fetch and normalize one match."""
        normalized, _ = self.get_match_with_raw(slug)
        return normalized


def normalize_match_payload(payload: Mapping[str, Any]) -> dict[str, Any]:
    """Normalize identity and common fields but preserve every raw APF statistic."""
    data = payload.get("data") if isinstance(payload.get("data"), Mapping) else payload
    if not isinstance(data, Mapping):
        raise APFError("APF match payload did not contain a data object")
    game = data.get("game")
    if not isinstance(game, Mapping):
        raise APFError("APF match payload did not contain a game object")

    teams: dict[str, Any] = {}
    for side in ("home", "away"):
        team = game.get(f"{side}Team")
        teams[side] = _entity(team)

    score_fields = (
        "homeScore",
        "awayScore",
        "homeScoreHalfTime",
        "awayScoreHalfTime",
        "homeScoreExtraTime",
        "awayScoreExtraTime",
        "homeScorePenalty",
        "awayScorePenalty",
        "homeShootOutScore",
        "awayShootOutScore",
    )
    match = {
        key: game[key]
        for key in (
            "id",
            "slug",
            "date",
            "time",
            "gameTime",
            "period",
            "status",
            "attendance",
        )
        if game.get(key) is not None
    }
    match.update(
        {
            "competition": _entity(game.get("competition")),
            "edition": _entity(game.get("edition")),
            "gameweek": _entity(game.get("gameweek")),
            "venue": _entity(game.get("venue")),
            "teams": teams,
            "score": {key: game[key] for key in score_fields if game.get(key) is not None},
        }
    )

    stats = data.get("stats") if isinstance(data.get("stats"), Mapping) else {}
    player_stats: list[dict[str, Any]] = []
    team_stats: dict[str, Any] = {}
    lineups: list[dict[str, Any]] = []
    lineup_data = data.get("lineups") if isinstance(data.get("lineups"), Mapping) else {}

    for side in ("home", "away"):
        team_id = _side_team_id(game, side)
        raw_team_stats = stats.get(f"{side}TeamStats")
        if isinstance(raw_team_stats, Mapping):
            team_stats[side] = raw_team_stats.get("stats", raw_team_stats)
        else:
            team_stats[side] = raw_team_stats or {}

        raw_players = stats.get(f"{side}TeamPlayersStats") or []
        if isinstance(raw_players, list):
            for entry in raw_players:
                if not isinstance(entry, Mapping):
                    continue
                raw_stats = entry.get("stats") if isinstance(entry.get("stats"), Mapping) else {}
                player_stats.append(
                    {
                        "id": entry.get("id"),
                        "side": side,
                        "team_id": team_id,
                        "player": _entity(entry.get("player")),
                        "canonical": canonical_player_stats(raw_stats),
                        "stats": dict(raw_stats),
                    }
                )

        raw_lineup = lineup_data.get(f"{side}Team") or []
        if isinstance(raw_lineup, Mapping):
            raw_lineup = raw_lineup.get("players") or raw_lineup.get("lineup") or []
        if isinstance(raw_lineup, list):
            for entry in raw_lineup:
                if not isinstance(entry, Mapping):
                    continue
                lineups.append(
                    {
                        "id": entry.get("id"),
                        "side": side,
                        "team_id": team_id,
                        "player": _entity(entry.get("player")),
                        "shirt_number": entry.get("shirtNumber"),
                        "starting": entry.get("starting"),
                        "captain": entry.get("captain"),
                        "manager": entry.get("manager"),
                        "formation_place": entry.get("formationPlace"),
                    }
                )

    raw_events = data.get("events")
    if isinstance(raw_events, Mapping):
        events = raw_events.get("data") or []
    elif isinstance(raw_events, list):
        events = raw_events
    else:
        events = []

    return {
        "source": "apf",
        "match": match,
        "events": events,
        "lineups": lineups,
        "team_stats": team_stats,
        "player_stats": player_stats,
    }
