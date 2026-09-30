"""Command-line interface for source extraction."""

from __future__ import annotations

import argparse
import json
import os
import sys
import tempfile
from pathlib import Path
from typing import Any, Mapping

from .apf import APFClient, APFError, payload_digest
from .apf_runs import capture_run


FINISHED_STATUSES = {"fulltime", "finished", "played", "afterextra", "afterpenalties"}


def _number(value: Mapping[str, Any]) -> Any:
    for key in ("week", "number", "gameweekNumber", "name"):
        if value.get(key) is not None:
            return value[key]
    return None


def _gameweek_id(value: Mapping[str, Any]) -> str | None:
    candidate = value.get("id") or value.get("gameweekId")
    return str(candidate) if candidate else None


def _matches(payload: Mapping[str, Any]) -> list[dict[str, Any]]:
    candidates: Any = payload.get("matches")
    if candidates is None and isinstance(payload.get("data"), Mapping):
        candidates = payload["data"].get("matches")
    return [dict(item) for item in (candidates or []) if isinstance(item, Mapping)]


def _match_slug(match: Mapping[str, Any]) -> str | None:
    value = match.get("slug")
    if not value and isinstance(match.get("game"), Mapping):
        value = match["game"].get("slug")
    return str(value) if value else None


def _match_status(match: Mapping[str, Any]) -> str:
    value = match.get("status")
    if value is None:
        value = match.get("period")
    if isinstance(value, Mapping):
        value = value.get("type") or value.get("name")
    return str(value or "").replace("_", "").replace("-", "").lower()


def _with_details(client: APFClient, round_payload: dict[str, Any], mode: str) -> dict[str, Any]:
    if mode == "none":
        return round_payload
    details = []
    for match in _matches(round_payload):
        if mode == "finished" and _match_status(match) not in FINISHED_STATUSES:
            continue
        slug = _match_slug(match)
        if slug:
            details.append(client.get_match(slug))
    result = dict(round_payload)
    result["match_details"] = details
    return result


def _find_gameweek(discovery: Mapping[str, Any], number: int) -> Mapping[str, Any]:
    for value in discovery.get("gameweeks") or []:
        if isinstance(value, Mapping) and str(_number(value)) == str(number):
            return value
    available = ", ".join(str(_number(item)) for item in discovery.get("gameweeks") or [])
    raise APFError(f"Gameweek {number} was not found. Available: {available}")


def _round(client: APFClient, discovery: Mapping[str, Any], number: int, details: str) -> dict[str, Any]:
    gameweek = _find_gameweek(discovery, number)
    gameweek_id = _gameweek_id(gameweek)
    if not gameweek_id:
        raise APFError(f"Gameweek {number} did not contain an id")
    payload = client.get_round(
        str(discovery["edition_id"]), str(discovery["round_id"]), gameweek_id
    )
    payload["requested_gameweek"] = dict(gameweek)
    return _with_details(client, payload, details)


def _season(client: APFClient, details: str) -> dict[str, Any]:
    discovery = client.discover()
    rounds = []
    for gameweek in discovery["gameweeks"]:
        if not isinstance(gameweek, Mapping):
            continue
        gameweek_id = _gameweek_id(gameweek)
        if not gameweek_id:
            continue
        payload = client.get_round(
            str(discovery["edition_id"]), str(discovery["round_id"]), gameweek_id
        )
        payload["requested_gameweek"] = dict(gameweek)
        rounds.append(_with_details(client, payload, details))
    result = {
        "source": "apf",
        "discovery": discovery,
        "rounds": rounds,
    }
    result["payload_sha256"] = payload_digest(result)
    return result


def _write_json(value: Any, output: str | None) -> None:
    document = json.dumps(value, ensure_ascii=False, indent=2) + "\n"
    if not output:
        sys.stdout.write(document)
        return
    path = Path(output)
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(document)
        os.replace(temporary, path)
    except Exception:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Extract official APF football data")
    parser.add_argument("--output", help="write JSON atomically to this path (default: stdout)")
    parser.add_argument("--delay", type=float, default=0.25, help="minimum delay between requests")
    subparsers = parser.add_subparsers(dest="command", required=True)

    subparsers.add_parser("discover", help="discover edition, round, and gameweek UUIDs")

    round_parser = subparsers.add_parser("round", help="extract one numbered gameweek")
    round_parser.add_argument("number", type=int)
    round_parser.add_argument(
        "--details", choices=("none", "finished", "all"), default="none"
    )

    match_parser = subparsers.add_parser("match", help="extract a match by APF slug")
    match_parser.add_argument("slug")

    season_parser = subparsers.add_parser("season", help="extract all discovered gameweeks")
    season_parser.add_argument(
        "--details", choices=("none", "finished", "all"), default="none"
    )

    run_parser = subparsers.add_parser("run", help="persist a durable APF extraction run")
    run_parser.add_argument("--rounds", required=True, help="comma-separated completed gameweeks, e.g. 5 or 4,5")
    run_parser.add_argument(
        "--runs-root", type=Path,
        default=Path("tests/sofascore_scraper/data/apf/clausura-2026/runs"),
        help="durable run root (the default is only suitable for local development)",
    )
    run_parser.add_argument(
        "--storage-uri", required=True,
        help="stable durable URI corresponding to --runs-root, e.g. s3://football-raw/apf/clausura-2026/runs",
    )
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    client = APFClient(delay=args.delay)
    try:
        if args.command == "discover":
            value = client.discover()
        elif args.command == "round":
            value = _round(client, client.discover(), args.number, args.details)
        elif args.command == "match":
            value = client.get_match(args.slug)
        elif args.command == "run":
            try:
                rounds = [int(value.strip()) for value in args.rounds.split(",") if value.strip()]
            except ValueError as error:
                raise APFError("--rounds must be a comma-separated list of positive integers") from error
            run_dir = capture_run(client, args.runs_root, rounds, storage_uri=args.storage_uri)
            _write_json({"run_dir": str(run_dir), "run_id": run_dir.name, "selected_rounds": sorted(rounds)}, args.output)
            return 0
        else:
            value = _season(client, args.details)
        _write_json(value, args.output)
        return 0
    except (APFError, OSError) as error:
        parser.exit(1, f"error: {error}\n")


if __name__ == "__main__":
    raise SystemExit(main())
