# Repository Guidelines

## Structure

This is a monorepo with two deployable applications. The Go web app uses
`cmd/web/` as its composition root; domain/application code lives in
`internal/leaderboard/`, and HTTP/templates/assets in `internal/web/`. The
independent Python APF ETL package lives in `etl/src/go_football_sources/` with
unit tests in `etl/tests/`. Database definitions are in `data/db/`; source
research and endpoint decisions belong in `data/sources/`.

## Commands

```bash
make test
make fmt
make vet
make infra-up
go run ./cmd/web
PYTHONPATH=etl/src python3 -m go_football_sources.cli discover
```

APF commands make live, rate-limited requests and are for manual or scheduled
checks, not routine unit tests.

## Go conventions

Use idiomatic Go, `gofmt`, focused packages, constructor-injected dependencies,
and `context.Context` for I/O. Keep HTTP concerns in `internal/web`; do not put
SQL in handlers. Wrap errors with operation context. Prefer the standard library
unless a dependency clearly improves infrastructure integration. Add table or
behavior tests next to changed packages.

## Python conventions

Use Python 3.11 syntax, four-space indentation, type annotations, and concise
public docstrings. Keep extraction dependency-free unless justified. Preserve
raw APF payloads while adding normalized stable fields, and raise `APFError` for
source or shape failures. Use `unittest`, deterministic fixtures, and client
subclasses/mocks rather than live calls.

## Data invariants

PostgreSQL is authoritative and Redis is rebuildable. Never join source entities
by display name. Preserve UUID aliases and raw snapshots. Keep `NULL` distinct
from zero. Scores are integer millipoints. Aggregate changes and outbox events
must be committed transactionally; consumers must be idempotent and
version-aware. Update `data/sources/` when endpoint assumptions change.

## Commits and pull requests

Use short imperative Conventional Commit subjects (for example,
`feat: add leaderboard fragment`). Keep commits focused. Pull requests should
explain behavior, list commands run, link issues, and include sanitized output
when extraction or rendering changes.
