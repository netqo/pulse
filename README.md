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

See [`TECHNICAL_DESIGN.md`](./TECHNICAL_DESIGN.md) for the architectural source of truth: every
component, technology, and schema decision, with the rationale documented inline.

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

The full Mermaid diagram and data-flow narrative live in
[`TECHNICAL_DESIGN.md`](./TECHNICAL_DESIGN.md#3-system-architecture).

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

Every choice, and every rejected alternative, is defended in
[Key design decisions](./TECHNICAL_DESIGN.md#14-key-design-decisions-defendable-rationale).

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

## Roadmap

Development proceeds in independently demonstrable phases, each with an explicit "done" checkpoint:

- **Phase 0** - Skeleton: repo, README, architecture diagram, minimal Fetcher, initial migration.
- **Phase 1** - Pipeline end to end: Fetcher -> Kafka -> Processor -> Postgres, historical seed, first justified index.
- **Phase 2** - SQL Playground: monaco editor, sandboxed query endpoint, table/chart toggle, shareable URLs.
- **Phase 3** - Alerting: independent service, configurable rules, Telegram + webhook delivery.
- **Phase 4** - Observability: postgres_exporter, Prometheus, committed Grafana dashboards.
- **Phase 5** - Replication + failover: primary + 2 replicas via Patroni, documented failover.
- **Phase 6** - Polish: audit triggers, materialized views, Helm chart, `v1.0.0`.

Full detail in [Execution roadmap](./TECHNICAL_DESIGN.md#13-execution-roadmap-by-phase).

## Topics

`postgresql` `database-engineering` `time-series` `market-data` `go` `golang` `kafka` `redpanda`
`streaming` `event-driven` `sqlc` `pgx` `partitioning` `replication` `high-availability` `patroni`
`pgbouncer` `redis` `react` `typescript` `sql-playground` `monaco-editor` `echarts` `prometheus`
`grafana` `observability` `docker-compose` `kubernetes` `helm` `distributed-systems`

## License

Released under the [MIT License](./LICENSE).
