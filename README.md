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

## Deployment

The service is deployed to **Render** using the Blueprint in
[`render.yaml`](render.yaml), which builds the final Docker stage
(`gcr.io/distroless/static-debian12:nonroot`). Templates, CSS, JavaScript and
club crests are `//go:embed`ed, so the container is self-contained and requires
no volume mounts.

```bash
# Render Dashboard → New → Blueprint → point at this repository.
```

After the Blueprint is applied, fill in the secrets it prompts for. Anything
marked `sync: false` in `render.yaml` must come from the dashboard and must never
be committed.

| Variable | Required | Notes |
| --- | --- | --- |
| `DATABASE_URL` | yes | Use the Neon `-pooler` endpoint so the serverless database does not exhaust its connection limit |
| `EDITION_ID` | yes | Already set in `render.yaml` |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_REDIRECT_URL` | optional | All three or none. `GOOGLE_REDIRECT_URL` must match the deployed domain |
| `SENTRY_DSN`, `SENTRY_TRACES_SAMPLE_RATE` | optional | Leave empty to disable Sentry |
| `REDIS_ADDR` | optional | See below — the service runs correctly without it |

`HTTP_ADDR` is set to `:10000` in the Blueprint because Render injects `PORT`
(default `10000`), and this service has no `${PORT}` expansion. `APP_ENV` is set
to `production`, which enables `COOKIE_SECURE` for session cookies over HTTPS.

### Health checks

The Blueprint targets **`/health/live`**, not `/health/ready`.

`/health/ready` also probes Redis and returns `503` when Redis is unreachable.
Since Redis is intentionally not provisioned, using it as the Render health
check would mark every deploy unhealthy. `/health/live` reports process
liveness only.

### Redis

Redis stays unprovisioned in production, and the service does not need it:

1. The Redis client is constructed lazily; startup never connects or pings.
2. The only pinger is `/health/ready`, which is not used as the health check.
3. `leaderboard.Service.rank` skips the Redis read entirely when
   `SCORING_MODEL_ID=default`, which is the configured value.
4. Nothing writes sorted sets — the outbox consumer was never implemented — so
   even with a live Redis, `Top` returns empty and the request falls through to
   PostgreSQL.

Set `REDIS_ADDR` (for example to an Upstash free endpoint) only if you want
`/health/ready` to report healthy as well.

### Migrations

Migrations are **not** applied by the container. They are only mounted into
`docker-entrypoint-initdb.d` for a fresh local Postgres volume in
`compose.yaml`. The runtime image is distroless and has no shell, so a
`psql`-based pre-deploy step would also fail.

Apply them out-of-band, in numeric order, once per environment:

```bash
psql "$DATABASE_URL" -f data/db/migrations/001_initial.sql
# ... through 007_provider_independent_ratings.sql
```

The `006` and `007` migrations retire v1/v2 formulas and activate v3. They are
**not idempotent** — the `INSERT` statements use fixed UUIDs with no
`ON CONFLICT` clause, so a second run fails on a primary-key violation and rolls
back its whole transaction. Check `select version, status, count(*) from
season_award_formula group by version, status;` before re-running.

### Google sign-in

Once the service has a domain:

1. Update `GOOGLE_REDIRECT_URL` to the deployed callback URL and set it in the
   Blueprint's dashboard values.
2. Register that redirect URI in Google Cloud Console.
3. Publish the OAuth consent screen. In "Testing" mode refresh tokens expire
   after seven days.

### Free tier behaviour

Render's free Hobby web services spin down after 15 minutes without inbound
traffic, and the first request afterwards takes about a minute while the service
wakes; 750 instance hours are included per workspace per month. On a free Neon
database the compute may also suspend after inactivity, which adds a further
cold start on the first query. Both are acceptable for a project site and are
removed by moving the service to a paid plan.

If a deploy stalls because Render does not detect the GitHub checks, change
`autoDeployTrigger` in `render.yaml` from `checksPass` to `commit`.

`buildFilter` restricts deploys to the paths the production image actually
contains (`cmd/`, `internal/`, `go.mod`, `go.sum`), so edits under `etl/`,
`tests/` or `docs/` do not consume build minutes.

### Rotating credentials

`.env` is gitignored and has never been committed. If its values were ever
exposed — including by being pasted into a chat — rotate the Neon password and
the Google client secret, then update them only in the Render dashboard.

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
