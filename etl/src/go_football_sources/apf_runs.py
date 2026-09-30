"""Durable, immutable APF extraction runs.

A run is deliberately a filesystem boundary: normalization must consume the
files named by ``manifest.json`` rather than responses still held in memory.
Configure ``--storage-uri`` to the durable, stable URI that production workers
and PostgreSQL records can use (for example ``s3://bucket/apf/...``).
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Mapping
from uuid import uuid4

from .apf import APFClient, APFError

FINISHED_STATUSES = {"fulltime", "finished", "played", "afterextra", "afterpenalties"}


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def _status(match: Mapping[str, Any]) -> str:
    value = match.get("status") or match.get("period")
    if isinstance(value, Mapping):
        value = value.get("type") or value.get("name")
    return str(value or "").replace("_", "").replace("-", "").lower()


def _matches(payload: Mapping[str, Any]) -> list[Mapping[str, Any]]:
    value: Any = payload.get("matches")
    if value is None and isinstance(payload.get("data"), Mapping):
        value = payload["data"].get("matches")
    return [item for item in value or [] if isinstance(item, Mapping)]


def _slug(match: Mapping[str, Any]) -> str | None:
    value = match.get("slug")
    if not value and isinstance(match.get("game"), Mapping):
        value = match["game"].get("slug")
    return str(value) if value else None


def gameweek_number(gameweek: Mapping[str, Any]) -> int:
    """Read a gameweek's position, tolerating the field names APF has used."""
    for key in ("week", "number", "gameweekNumber"):
        if gameweek.get(key) is not None:
            return int(gameweek[key])
    raise APFError(f"APF gameweek has no numeric week: {gameweek!r}")


def _atomic_write(path: Path, body: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f".{path.name}.{uuid4().hex}.tmp")
    try:
        with temporary.open("xb") as handle:
            handle.write(body)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()


def _sync_directory(path: Path) -> None:
    # Directory fsync makes the preceding renames durable on POSIX filesystems.
    try:
        descriptor = os.open(path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def _uri(base_uri: str, relative_path: str) -> str:
    return base_uri.rstrip("/") + "/" + relative_path.replace(os.sep, "/")


def capture_run(
    client: APFClient,
    runs_root: Path,
    selected_rounds: list[int],
    *,
    storage_uri: str,
) -> Path:
    """Fetch selected rounds and publish an all-or-nothing immutable run.

    No target path is reused. Files are first fsynced in a private staging
    directory; only a complete run with a fsynced manifest is renamed into the
    public ``runs_root``. A failed fetch therefore cannot be normalized.
    """
    if not selected_rounds or any(number < 1 for number in selected_rounds):
        raise APFError("At least one positive selected round is required")
    if len(set(selected_rounds)) != len(selected_rounds):
        raise APFError("Selected rounds must not contain duplicates")
    if not storage_uri or "://" not in storage_uri:
        raise APFError("storage_uri must be a stable storage URI (for example s3://bucket/apf)")

    runs_root.mkdir(parents=True, exist_ok=True)
    run_id = f"{datetime.now(timezone.utc):%Y%m%dT%H%M%S%fZ}-{uuid4().hex[:12]}"
    destination = runs_root / run_id
    staging = runs_root / f".{run_id}.staging"
    if destination.exists() or staging.exists():
        raise APFError(f"Refusing to overwrite APF run {run_id}")
    staging.mkdir(mode=0o750)
    started_at = _utc_now()
    records: list[dict[str, Any]] = []

    def persist(
        relative_path: str,
        body: bytes,
        *,
        kind: str,
        source_url: str,
        source_entity_id: str,
        extra: Mapping[str, Any] | None = None,
    ) -> None:
        path = staging / relative_path
        if path.exists():
            raise APFError(f"Refusing to overwrite hash-addressed APF file {relative_path}")
        _atomic_write(path, body)
        record = {
            "kind": kind,
            "path": relative_path,
            "body_uri": _uri(storage_uri, f"{run_id}/{relative_path}"),
            "source_url": source_url,
            "source_entity_id": source_entity_id,
            "sha256": hashlib.sha256(body).hexdigest(),
            "bytes": len(body),
        }
        if extra:
            record.update(extra)
        records.append(record)

    try:
        discovery, body, discovery_url = client.discover_with_raw()
        persist(
            "discovery.html",
            body,
            kind="discovery",
            source_url=discovery_url,
            source_entity_id=str(discovery["edition_id"]),
            extra={"fetched_at": discovery["fetched_at"]},
        )
        gameweeks = {
            gameweek_number(item): item
            for item in discovery["gameweeks"]
            if isinstance(item, Mapping)
        }
        result_counts: dict[str, int] = {}
        match_ids: list[str] = []
        for number in sorted(selected_rounds):
            gameweek = gameweeks.get(number)
            if not gameweek or not gameweek.get("id"):
                raise APFError(f"APF discovery did not contain selected round {number}")
            gameweek_id = str(gameweek["id"])
            round_payload, round_body, round_url = client.get_round_with_raw(
                str(discovery["edition_id"]), str(discovery["round_id"]), gameweek_id
            )
            round_hash = hashlib.sha256(round_body).hexdigest()
            persist(
                f"gameweeks/{number}-{round_hash}.json",
                round_body,
                kind="gameweek",
                source_url=round_url,
                source_entity_id=gameweek_id,
                extra={"gameweek": number, "fetched_at": round_payload["fetched_at"]},
            )
            finished = 0
            for fixture in _matches(round_payload):
                if _status(fixture) not in FINISHED_STATUSES:
                    continue
                slug = _slug(fixture)
                if not slug:
                    raise APFError(f"Finished match in round {number} did not have a slug")
                normalized, _, match_body, match_url = client.get_match_with_raw_bytes(slug)
                match = normalized["match"]
                match_id = str(match.get("id") or fixture.get("id") or "")
                if not match_id:
                    raise APFError(f"Finished APF match {slug} did not have an id")
                if str((match.get("edition") or {}).get("id")) != str(discovery["edition_id"]):
                    raise APFError(f"Finished APF match {match_id} belongs to another edition")
                digest = hashlib.sha256(match_body).hexdigest()
                persist(
                    f"matches/{match_id}-{digest}.json",
                    match_body,
                    kind="match",
                    source_url=match_url,
                    source_entity_id=match_id,
                    extra={
                        "gameweek": number,
                        "slug": slug,
                        "fetched_at": normalized["fetched_at"],
                    },
                )
                match_ids.append(match_id)
                finished += 1
            result_counts[str(number)] = finished

        finished_at = _utc_now()
        manifest = {
            "schema_version": 1,
            "source": "apf",
            "run_id": run_id,
            "started_at": started_at,
            "finished_at": finished_at,
            "storage_uri": storage_uri.rstrip("/"),
            "edition_id": str(discovery["edition_id"]),
            "round_id": str(discovery["round_id"]),
            "selected_rounds": sorted(selected_rounds),
            "gameweek_ids": {
                str(number): str(gameweeks[number]["id"]) for number in sorted(selected_rounds)
            },
            "match_ids": match_ids,
            "result_counts": result_counts,
            "responses": records,
        }
        _atomic_write(
            staging / "manifest.json",
            (json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode(),
        )
        _sync_directory(staging)
        os.rename(staging, destination)
        _sync_directory(runs_root)
        return destination
    except Exception:
        shutil.rmtree(staging, ignore_errors=True)
        raise


def read_manifest(run_dir: Path) -> dict[str, Any]:
    """Read and validate a completed run before normalization/loading."""
    try:
        manifest = json.loads((run_dir / "manifest.json").read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise APFError(f"Could not read APF run manifest from {run_dir}") from error
    if not isinstance(manifest, dict) or manifest.get("run_id") != run_dir.name:
        raise APFError("APF run manifest has an invalid run id")
    responses = manifest.get("responses")
    if not isinstance(responses, list) or not responses:
        raise APFError("APF run manifest did not contain responses")
    for record in responses:
        if not isinstance(record, Mapping):
            raise APFError("APF run manifest contains an invalid response record")
        relative_path, digest = record.get("path"), record.get("sha256")
        if (
            not isinstance(relative_path, str)
            or Path(relative_path).is_absolute()
            or ".." in Path(relative_path).parts
        ):
            raise APFError("APF run manifest contains an unsafe response path")
        try:
            body = (run_dir / relative_path).read_bytes()
        except OSError as error:
            raise APFError(f"APF run response is missing: {relative_path}") from error
        if hashlib.sha256(body).hexdigest() != digest:
            raise APFError(f"APF run response hash mismatch: {relative_path}")
    return manifest
