# Pulse

> **Market Data Pipeline + SQL Playground.** A self-hosted market-data platform where the database is a first-class citizen: run raw SQL against the full history, watch a live dashboard without writing a line, and configure price alerts, all on top of a distributed backend with streaming replication and end-to-end observability.

<!-- Status and license -->
![Status](https://img.shields.io/badge/status-pre--release-orange)
![Version](https://img.shields.io/badge/version-v0.0.0-lightgrey)
![License](https://img.shields.io/badge/license-MIT-blue)
![PRs welcome](https://img.shields.io/badge/PRs-welcome-brightgreen)
![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-yellow)

<!-- Core stack -->
![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16+-4169E1?logo=postgresql&logoColor=white)
![Redpanda](https://img.shields.io/badge/Redpanda-Kafka%20API-E14225?logo=apachekafka&logoColor=white)
![Redis](https://img.shields.io/badge/Redis-7.x-DC382D?logo=redis&logoColor=white)
![React](https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=black)
![TypeScript](https://img.shields.io/badge/TypeScript-5.x-3178C6?logo=typescript&logoColor=white)

<!-- Infra and ops -->
![Docker](https://img.shields.io/badge/Docker-Compose%20v2-2496ED?logo=docker&logoColor=white)
![Kubernetes](https://img.shields.io/badge/Kubernetes-Helm%203-326CE5?logo=kubernetes&logoColor=white)
![Patroni](https://img.shields.io/badge/HA-Patroni%20%2B%20etcd-2088FF)
![Prometheus](https://img.shields.io/badge/Prometheus-metrics-E6522C?logo=prometheus&logoColor=white)
![Grafana](https://img.shields.io/badge/Grafana-dashboards-F46800?logo=grafana&logoColor=white)
![GitHub Actions](https://img.shields.io/badge/CI-GitHub%20Actions-2088FF?logo=githubactions&logoColor=white)

---

## What this is

A single-operator, self-hosted platform that ingests public crypto market data, stores it in a
carefully engineered PostgreSQL schema, and serves it through a REST API, a live dashboard, and an
in-browser SQL Playground. The thesis is *native Go, no unnecessary layers*: every technology in the
stack earns its place against a concrete requirement, and the database is treated as an engineering
artifact, not an afterthought.

It is **not** a trading platform (no order execution, no financial advice, no real money) and **not**
a multi-tenant SaaS. It exists to showcase database and distributed-backend engineering end to end.

## Highlights

- **Database as a first-class citizen.** Range-partitioned time-series fact table, justified indexing
  backed by `EXPLAIN ANALYZE` before/after, materialized read models, audit triggers, and versioned
  migrations with tested rollback.
- **Decoupled, observable pipeline.** Producers and consumers separated by a durable event log
  (Kafka protocol via Redpanda), with independent consumer groups for the write path and the
  alerting path so neither blocks the other.
- **In-browser SQL Playground.** Run arbitrary read-only SQL against the full history through a
  layered sandbox (replica-only, least-privilege role, statement timeout, row cap, read-only
  transaction, rate limiting), then toggle between table and chart (line / bar / candlestick).
- **High availability.** One primary plus two replicas managed by Patroni + etcd, with streaming
  replication and read/write split through pgBouncer, plus a documented failover simulation.
- **Reproducible in one command.** `docker compose up` brings up the entire stack locally; a Helm
  chart packages it for Kubernetes.

## Architecture at a glance

```
External sources        Ingestion pipeline           Storage                Serving        Clients
----------------        ------------------           -------                -------        -------
Binance WebSocket  -->  Fetcher --> Redpanda  -->  Processor --> Postgres  --> API + -->  Dashboard
data.binance.vision              (market.ticks)    Alerting     (primary +     pgBouncer   SQL Playground
CoinGecko (metadata)                                            2 replicas)                External consumers
                                                    Redis cache
```

Four Go services, each with a single responsibility:

| Service | Responsibility |
|---|---|
| **Fetcher** | Ingestion boundary: streams live ticks from Binance WebSocket, seeds deep history from `data.binance.vision` archives, backfills gaps via REST, polls CoinGecko for metadata. Produces to Kafka. |
| **Processor** | Consumes `market.ticks`, computes derived metrics (moving averages, volatility), batch-writes to the partitioned `prices` table. |
| **Alerting** | Consumes the same stream on an independent consumer group, evaluates user rules, dispatches Telegram / webhook notifications, persists history. |
| **API** | REST endpoints for the frontend and external consumers, read/write split via pgBouncer, Redis caching, the sandboxed Playground query endpoint. |

## Tech stack

| Layer | Choice |
|---|---|
| Backend services | Go 1.23+ |
| Event streaming | Kafka protocol via Redpanda |
| HTTP / API | REST over `net/http` + `chi` |
| Database access | `pgx` + `sqlc` (no ORM) |
| Schema migrations | `golang-migrate` (raw `.up.sql` / `.down.sql`) |
| Cache | Redis 7.x |
| Data sources | Binance WebSocket + `data.binance.vision` + CoinGecko Demo |
| Charting | Apache ECharts |
| Frontend | React 18 + TypeScript 5 + monaco-editor |
| Database | PostgreSQL 16+ |
| HA / clustering | Patroni 3 + etcd |
| Pooling / read-write split | pgBouncer |
| Metrics | Prometheus + postgres_exporter |
| Dashboards | Grafana (JSON in repo) |
| Local orchestration | Docker Compose v2 |
| Kubernetes packaging | Helm 3 |
| CI/CD | GitHub Actions |

Every technology is a deliberate choice, weighed against its alternatives, with the thesis of a
native Go backend and the database as the centerpiece.

## Getting started

> The full local stack is being built out phase by phase (see the roadmap). The target developer
> experience is:

```bash
# Bring up the entire stack: Postgres (primary + replicas + Patroni + etcd),
# Redpanda, Redis, the four Go services, pgBouncer, Prometheus, Grafana, and the frontend.
docker compose up

# Apply database migrations.
make migrate

# Run tests and linters.
make test
make lint
```

## Historical backfill

The `seed` job backfills the `prices` table from the public
[data.binance.vision](https://data.binance.vision) monthly kline archives. It
downloads each month, verifies its SHA256 checksum, derives the same rolling
indicators the processor computes live, and writes the enriched rows one month
at a time. Re-running a month replaces it (a delete and bulk copy in a single
transaction), and the rolling window is warmed from the closes already stored
just before the range, so a re-run is fully idempotent: it reproduces the same
rows and the same indicator values.

```bash
# Seed BTCUSDT and ETHUSDT for Q1 2024 into the running stack.
docker compose run --rm seed -symbols BTCUSDT,ETHUSDT -from 2024-01 -to 2024-03
```

Use a consistent `-interval` for a given range: the `prices` table records
observations, not candle granularity, so re-seeding a range with a different
interval replaces its rows at the new resolution.

## HTTP API

The `api` service exposes read-only endpoints over the enriched price data. The
public API listens on `:8080` (mapped to host `:8081` by default); health and
Prometheus metrics live on a separate internal listener at `:9103`.

| Method and path | Description |
| --- | --- |
| `GET /api/v1/instruments` | List every tracked instrument. |
| `GET /api/v1/instruments/{symbol}/latest` | Most recent price observation for a symbol. |
| `GET /api/v1/instruments/{symbol}/prices?from=&to=&limit=` | Historical series in the half-open range `[from, to)`, returned oldest first. `from`/`to` are RFC3339 (default: the last 24h); `limit` defaults to 1000 and is capped at 5000. When more observations exist than `limit`, the most recent ones in the range are returned. |

Symbols are case-insensitive. Monetary values are returned as decimal strings to
preserve exact precision. Unknown symbols return `404`; invalid query parameters
return `400`.

```bash
curl -s localhost:8081/api/v1/instruments/BTCUSDT/latest
curl -s "localhost:8081/api/v1/instruments/BTCUSDT/prices?from=2025-06-01T00:00:00Z&to=2025-06-02T00:00:00Z&limit=500"
```

### SQL Playground

`POST /api/v1/playground/query` executes read-only SQL and returns the columns
and rows as JSON (numeric and timestamp values as strings to preserve precision).

```bash
curl -s localhost:8081/api/v1/playground/query \
  -H 'content-type: application/json' \
  -d '{"query":"SELECT symbol, base_asset FROM instruments ORDER BY symbol LIMIT 5"}'
```

Because it runs arbitrary user SQL, the endpoint is sandboxed in independent
layers, so no single failure exposes the database:

- **Dedicated, bounded pool.** The Playground has its own connection pool with a
  small `MaxConns`, isolated from the pool serving the rest of the API, so a
  burst of slow queries can neither exhaust nor poison the shared connections.
- **Least-privilege role.** The query runs as `playground_readonly` (assumed via
  `SET LOCAL ROLE`), which holds `SELECT` on a whitelist of tables and nothing
  else -- no DDL, no DML, no access to any other object. Table and function
  privileges are checked at plan time as this role, so a query that tries to
  escalate its role mid-execution still cannot read a non-whitelisted object.
- **Read-only transaction.** `BEGIN READ ONLY` rejects every write, including
  data-modifying CTEs, regardless of the role's grants.
- **Statement timeout.** A per-transaction `statement_timeout` cancels a runaway
  query.
- **Connection reset.** Each connection is reset (`DISCARD ALL`) after every
  query, so session-scoped state (advisory locks, prepared statements, temp
  objects) cannot leak into the next execution.
- **Row and byte caps.** The query is wrapped in a bounded subquery, capping the
  rows returned (and turning any multi-statement input into a syntax error), plus
  a hard cap on the bytes streamed back.
- **Per-IP rate limiting.** The endpoint is throttled per client IP (by the
  connection's remote address, not a spoofable `X-Forwarded-For` header).

A static pre-check rejects obviously non-read statements early for a clearer
error, but the transaction and role are the real enforcement. Invalid or rejected
queries return `400` with the PostgreSQL error. These layers are defense in
depth: each is independent, so exposing the endpoint does not rest on any single
control. (Replica-only routing is added in the replication phase, which removes
even read load from the primary.)

#### Saving and sharing

A query can be saved and shared through a non-enumerable UUID:

| Method and path | Description |
| --- | --- |
| `POST /api/v1/playground/save` | Persist a query, returning its id and load path. |
| `GET /api/v1/playground/q/{id}` | Load a saved query by id. |

```bash
# Save a query (optional title and opaque chart_config for the frontend).
curl -s localhost:8081/api/v1/playground/save \
  -H 'content-type: application/json' \
  -d '{"query":"SELECT symbol FROM instruments ORDER BY symbol","title":"symbols","chart_config":{"type":"bar"}}'
# -> {"id":"<uuid>","url":"/api/v1/playground/q/<uuid>"}

# Load it back.
curl -s localhost:8081/api/v1/playground/q/<uuid>
```

The saved query must itself pass the read-only validation, so only a query
shaped like a read is stored (the check gates the leading keyword, not full SQL
validity). The UUID primary key makes the share URL non-guessable, and
`saved_queries` is deliberately excluded from the `playground_readonly` grants,
so sandboxed SQL cannot read back other users' saved queries. Save is throttled
per IP; loading by id is a cheap indexed lookup. A malformed id returns `400`,
an unknown id `404`. Saved queries currently accumulate without a retention or
row-cap policy; adding one (TTL or a bounded count) is deferred to a later phase.

## Roadmap

Development proceeds in independently demonstrable phases, each with an explicit "done" checkpoint:

- **Phase 0** - Skeleton: repo, README, architecture diagram, minimal Fetcher, initial migration.
- **Phase 1** - Pipeline end to end: Fetcher -> Kafka -> Processor -> Postgres, historical seed, first justified index.
- **Phase 2** - SQL Playground: monaco editor, sandboxed query endpoint, table/chart toggle, shareable URLs.
- **Phase 3** - Alerting: independent service, configurable rules, Telegram + webhook delivery.
- **Phase 4** - Observability: postgres_exporter, Prometheus, committed Grafana dashboards.
- **Phase 5** - Replication + failover: primary + 2 replicas via Patroni, documented failover.
- **Phase 6** - Polish: audit triggers, materialized views, Helm chart, `v1.0.0`.

## Topics

`postgresql` `database-engineering` `time-series` `market-data` `go` `golang` `kafka` `redpanda`
`streaming` `event-driven` `sqlc` `pgx` `partitioning` `replication` `high-availability` `patroni`
`pgbouncer` `redis` `react` `typescript` `sql-playground` `monaco-editor` `echarts` `prometheus`
`grafana` `observability` `docker-compose` `kubernetes` `helm` `distributed-systems`

## License

Released under the [MIT License](./LICENSE).
