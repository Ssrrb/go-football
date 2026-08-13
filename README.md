# Go Football

Real-time player rankings for the Paraguayan Primera División, built from the
official APF data feed.

## Stack

- **Go + server-rendered HTML + HTMX** for the web application
- **Python** for independent APF extraction and ETL jobs
- **PostgreSQL 16** as the source of truth
- **Redis sorted sets** as a rebuildable real-time leaderboard projection
- **Docker Compose** for local infrastructure

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
```

## Quick start

Requirements: Go 1.23+, Python 3.11+, and Docker.

```bash
cp .env.example .env
make infra-up
make test
make run
```

Open <http://localhost:8080>. Set `EDITION_ID` and `SCORING_MODEL_ID` in `.env`
after loading and scoring an edition. Local registration and password login work
without any third-party credentials. To run everything in containers:

```bash
make dev
```

PostgreSQL bootstrap migrations run only when its Docker volume is first
created. Use `docker compose down -v` to intentionally recreate a local database.
For an existing database, apply each outstanding migration once in numeric order.
For example, to add the normalized SofaScore player rating:

```bash
psql "$DATABASE_URL" -f data/db/migrations/004_player_match_rating.sql
```

## Authentication

The web app supports email/password accounts and optional Google OAuth. Passwords
are bcrypt-hashed, while only SHA-256 hashes of opaque session tokens are stored
in PostgreSQL. Login, registration, and logout forms use CSRF tokens and work
with both standard form submissions and HTMX.

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
| `GET /?position=GK` | Full leaderboard page, or an HTMX fragment when `HX-Request: true` |
| `GET/POST /register` | Local account registration form and submission |
| `GET/POST /login` | Local login form and submission |
| `POST /logout` | Revoke the current session |
| `GET /auth/google` | Start Google OAuth when configured |
| `GET /auth/google/callback` | Google OAuth callback |
| `GET /health/live` | Process liveness |
| `GET /health/ready` | PostgreSQL and Redis readiness |

The detailed data model, correction rules, Redis keys, and outbox invariants are
in [`data/data-model.md`](data/data-model.md). The system design is in
[`docs/architecture.md`](docs/architecture.md).
