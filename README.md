# Go Football

Real-time player rankings for the Paraguayan Primera División, built from the
official APF data feed.

## Stack

- **Go + server-rendered HTML + HTMX** for the web application
- **Python** for independent APF extraction and ETL jobs
- **PostgreSQL 16** as the source of truth
- **In-process ranking cache** as the read layer for the default scoring model
- **Docker Compose** for local infrastructure

### Redis status

Redis is **not** currently a read model for the leaderboard. The client is
configured, connected, and pinged by `/health/ready`, but no process writes
sorted sets: the outbox consumer described in
[`data/data-model.md`](data/data-model.md) has not been implemented, so a
`ZADD` is never issued. The default scoring model (`SCORING_MODEL_ID=default`)
computes the ranking from finished-match facts in PostgreSQL and is served from
a short-lived in-process cache (`LEADERBOARD_CACHE_TTL`, default 15s). Treat
Redis as reserved capacity, not a working cache.

## Repository layout

```text
cmd/web/                 Go web process entry point
internal/config/         Environment configuration
internal/leaderboard/    PostgreSQL/Redis leaderboard application layer
internal/web/            HTTP handlers, templates, and static assets
etl/                     Standalone Python APF ETL package and tests
data/db/                 Schema and ordered SQL migrations
data/sources/            Source research and extraction decisions
docs/architecture.md     Components and data flow
docs/season-awards.md    Versioned season-award methodology
```

## Quick start

Requirements: Go 1.23+, Python 3.11+, and Docker.

```bash
cp .env.example .env
make infra-up
make test
make run
```

Open <http://localhost:8080>. `make run` watches Go files, templates, CSS, and
JavaScript and automatically rebuilds and restarts the service after a change;
refresh the browser without restarting the terminal. Set `EDITION_ID` in `.env`.
The default `SCORING_MODEL_ID=default` recalculates the ranking from finished
matches without using a provider-published rating. It robustly normalizes five
statistical dimensions within each position, applies position-specific weights,
and reduces small-sample inflation with `minutes/(minutes+900)`. Set it to a
`scoring_model` UUID
after publishing custom scores instead. Local registration and password login
work without any third-party credentials. To run everything in containers with
the same live-reload behavior:

```bash
make dev
```

The first run downloads the pinned [Air](https://github.com/air-verse/air)
development watcher. Changes under `cmd/` and `internal/` are then picked up
automatically. If a build fails, fix the reported error and Air will retry on
the next file change.

PostgreSQL bootstrap migrations run only when its Docker volume is first
created. Use `docker compose down -v` to intentionally recreate a local database.
For an existing database, apply each outstanding migration once in numeric order.
For example, to apply the season-award formula history and activate the
provider-independent v3 formulas:

```bash
psql "$DATABASE_URL" -f data/db/migrations/005_season_awards.sql
psql "$DATABASE_URL" -f data/db/migrations/006_season_awards_v2.sql
psql "$DATABASE_URL" -f data/db/migrations/007_provider_independent_ratings.sql
```

The migrations retain immutable v1/v2 formulas and activate v3 while preserving
auditable, versioned selections. See
[`docs/season-awards.md`](docs/season-awards.md) for
weights, eligibility, progression windows, and tie-breakers.

## Authentication

The web app supports email/password accounts and optional Google OAuth. The
leaderboard, classification, and methodology pages require an active session;
visitors without one are redirected to `/login`, where they can sign in or go to
registration. Passwords are bcrypt-hashed, while only SHA-256 hashes of opaque
session tokens are stored in PostgreSQL. Login, registration, and logout forms
use CSRF tokens and work with both standard form submissions and HTMX.

To enable Google sign-in:

1. In Google Cloud Console, create an **OAuth 2.0 Client ID** with application
   type **Web application**.
2. Add `http://localhost:8080/auth/google/callback` as an authorized redirect URI
   (use the HTTPS callback URL for production).
3. Set all three values in `.env`:

```bash
GOOGLE_CLIENT_ID=your-client-id.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=your-client-secret
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
```

Google sign-in is hidden when those values are empty. They must either all be set
or all be empty. Set `COOKIE_SECURE=true` behind HTTPS; it defaults to true when
`APP_ENV=production`. `SESSION_DURATION` defaults to `720h` (30 days). Never
commit OAuth credentials or put the client secret in browser code.

## Sentry

Create a Sentry project with the **Go** platform for the web service, then set
its server-side DSN in the deployment environment:

```bash
SENTRY_DSN=https://examplePublicKey@o0.ingest.sentry.io/0
SENTRY_TRACES_SAMPLE_RATE=0.1
SENTRY_RELEASE=$(git rev-parse HEAD)
```

Leave `SENTRY_DSN` empty to disable Sentry (the local default). The Go service
reports startup failures, request panics, and handled leaderboard/rendering
errors. Set the trace sample rate to `0` for errors only or to a value from `0`
to `1` to sample request performance traces. Use a separate **Python** Sentry
project if the independently deployed ETL needs monitoring.

## ETL

The Python application is isolated under `etl/` and has no runtime dependency on
the Go process.

```bash
PYTHONPATH=etl/src python3 -m go_football_sources.cli discover
PYTHONPATH=etl/src python3 -m go_football_sources.cli round 4 --details finished
PYTHONPATH=etl/src python3 -m unittest discover -s etl/tests -v
```

Live APF commands are rate-limited and intended for manual checks or scheduled
jobs, not unit tests. See [`etl/README.md`](etl/README.md) and the
[source decision](data/sources/extraction.md).

## HTTP endpoints

| Route | Purpose |
| --- | --- |
| `GET /?position=GK` | Authenticated leaderboard page, or an HTMX fragment when `HX-Request: true` |
| `GET/POST /register` | Local account registration form and submission |
| `GET/POST /login` | Local login form and submission |
| `POST /logout` | Revoke the current session |
| `GET /auth/google` | Start Google OAuth when configured |
| `GET /auth/google/callback` | Google OAuth callback |
| `GET /health/live` | Process liveness |
| `GET /health/ready` | PostgreSQL and Redis connectivity (not proof of a populated cache) |

## Database latency

Leaderboard reads go to a single remote PostgreSQL, so a query costs one network
round trip even when it executes in tens of milliseconds. Two settings keep
that cost off the request path:

- `DB_MIN_CONNS` (default 2) keeps that many connections open and warm, so the
  first request after a restart does not pay a TLS handshake.
- `LEADERBOARD_CACHE_TTL` (default 15s) collapses concurrent readers of the same
  ranking onto a single query. Set it to `0` to disable.
- `SEASON_AWARDS_CACHE_TTL` (default 60s) does the same for the season-award
  projection, which is far more expensive than the leaderboard. Set it to `0` to
  disable.

The season-award projection never runs inside a document render. The overview
page ships the award card shells and `GET /premios` fills them in after first
paint, so the leaderboard is not queued behind it. See
[`docs/season-awards.md`](docs/season-awards.md).

Every authenticated request still resolves its session in PostgreSQL, which is
one read plus one throttled touch. That round trip, not the leaderboard, is now
the floor for a page load.

The detailed data model, correction rules, and the (not yet implemented) Redis
projection and outbox invariants are in
[`data/data-model.md`](data/data-model.md). The system design is in
[`docs/architecture.md`](docs/architecture.md).
