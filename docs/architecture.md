# Architecture Deep-Dive

## Why this design for a wealth management factsheet?

### Read vs Write patterns

A factsheet page is **overwhelmingly read-heavy**. Thousands of users might view the Prisma page during a day; the data only changes once (after market close). This asymmetry drives every design decision:

- **Pre-compute everything at write time** (ETL), keep reads simple (SELECT).
- **No JOIN-heavy queries on the hot path** — exposure tables store denormalised aggregates.
- **pgx connection pool** handles concurrent reads without spawning new DB connections per request.

### Separation of concerns: API vs ETL

```
ETL pipeline (Python)     ←→     PostgreSQL     ←→     API Server (Go)
  - I/O heavy                    (single source       - CPU / network
  - Pandas/numpy math              of truth)            bound
  - Tolerates slowness           - ACID guarantees    - Latency sensitive
  - Runs once/day                - Indexed reads      - Stateless
```

If you put the Yahoo Finance calls inside the Go API, every factsheet request would incur:
- 6 network calls to Yahoo Finance (one per ETF)
- Float-heavy pandas computation
- Risk of partial failures mid-response

Pre-computation avoids all of this.

### Why PostgreSQL over a time-series DB?

For a **single portfolio** with daily granularity, the volume is tiny:
- NAV history: ~250 rows/year
- Exposures: ~20 rows/day

A time-series DB (InfluxDB, TimescaleDB) would add operational complexity for no benefit. PostgreSQL with a `(product_id, date DESC)` index handles this easily and gives us JOINs for free when we need them.

If Spring Street scales to 100+ products with intraday tick data, adding TimescaleDB or partitioning nav_history by year would be the natural next step.

### Idempotent ETL

Every upsert uses `ON CONFLICT DO UPDATE`. This means:
- You can run the pipeline twice on the same day safely.
- After a failure, you just re-run — no cleanup needed.
- Backfill is easy: just run with a custom date range.

### Data quality: NUMERIC vs FLOAT

All monetary and percentage values use PostgreSQL `NUMERIC(p, s)` rather than `FLOAT`. This avoids IEEE 754 rounding errors like `0.1 + 0.2 = 0.30000000000000004`. In financial systems, this is non-negotiable.

### Graceful degradation

When Yahoo Finance data is unavailable for a ticker, the exposure calculator falls back to curated profiles sourced from public factsheets. The API always returns data — either fresh or last-known-good. The staleness is surfaced via the `as_of_date` field on every response.

## Scaling considerations (for future)

| Concern | Current approach | At scale |
|---------|-----------------|---------|
| Many products | Single pipeline loop | Parallel workers per product |
| Intraday updates | Daily only | WebSocket feed (Alpaca, Polygon.io) |
| API caching | None (DB is fast enough) | Redis cache with 1-hour TTL |
| Auth | None (add middleware) | JWT + API key for admin routes |
| Monitoring | etl_run_log table | Prometheus + Grafana |
