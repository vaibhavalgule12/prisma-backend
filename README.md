# Prisma Factsheet Backend

Backend system powering the [Prisma Global Growth](https://springstreet.in/products/prisma/global-growth-prisma) product factsheet.

---

## Table of Contents

1. [Architecture Overview](#architecture-overview)
2. [System Design Decisions](#system-design-decisions)
3. [Database Schema](#database-schema)
4. [API Reference](#api-reference)
5. [ETL Pipeline](#etl-pipeline)
6. [Setup & Running Locally](#setup--running-locally)
7. [Project Structure](#project-structure)
8. [Data Freshness Strategy](#data-freshness-strategy)

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        CLIENT / FRONTEND                        │
│                  (springstreet.in/products/...)                  │
└───────────────────────────┬─────────────────────────────────────┘
                            │ HTTPS REST
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                     GO API SERVER (Chi)                         │
│                                                                 │
│  GET /api/v1/products/{slug}/factsheet  ◄── Main endpoint       │
│  GET /api/v1/products/{slug}/nav                                │
│  GET /api/v1/products/{slug}/holdings                           │
│  GET /api/v1/products/{slug}/exposures                          │
│  POST /api/v1/admin/sync               ◄── Trigger ETL          │
└───────────────────────────┬─────────────────────────────────────┘
                            │ pgx/v5 connection pool
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                     POSTGRESQL 16                               │
│                                                                 │
│  products            nav_history          holdings              │
│  constituent_etfs    performance_metrics  sector_exposure       │
│  country_exposure    market_cap_exposure  etl_run_log           │
└───────────────────────────▲─────────────────────────────────────┘
                            │ psycopg2 upserts
                            │
┌───────────────────────────┴─────────────────────────────────────┐
│                   PYTHON ETL SERVICE                            │
│                                                                 │
│  ┌──────────────────┐    ┌──────────────────────────────────┐   │
│  │  Yahoo Finance   │    │  Exposure Calculator             │   │
│  │  yfinance lib    │    │  (sector / country / market cap) │   │
│  └────────┬─────────┘    └──────────────┬───────────────────┘   │
│           │                             │                       │
│           └─────────────┬───────────────┘                       │
│                         ▼                                       │
│               Pipeline Orchestrator                             │
│           (schedule: weekdays 18:30)                            │
└─────────────────────────────────────────────────────────────────┘
```

### Component Roles

| Component | Language | Responsibility |
|-----------|----------|----------------|
| **API Server** | Go 1.22 | Serves read-heavy REST API with connection pooling and graceful shutdown |
| **ETL Service** | Python 3.12 | Fetches market data, computes metrics, upserts to DB daily |
| **PostgreSQL 16** | SQL | Source of truth for all financial data |

---

## System Design Decisions

### Why separate Go API + Python ETL?

- **Go** excels at low-latency, high-concurrency HTTP — ideal for a read-heavy factsheet API that may be called many times per page load.
- **Python** has the richest financial data ecosystem (`yfinance`, `pandas`, `numpy`) — ideal for the ETL/analytics layer.
- They share PostgreSQL as the integration layer. This is a common pattern in fintech stacks (FastAPI/Django for data, Go for high-performance APIs).

### Pre-computation over live calculation

All exposure metrics (sector, country, market cap) and performance metrics are **pre-computed by the ETL pipeline and stored**. The API reads pre-computed rows — it never talks to Yahoo Finance directly. This means:

- API responses are **fast** (single DB read, no external I/O)
- No rate-limit exposure on the API hot path
- Data is consistent across concurrent requests

### UPSERT-first data model

Every ETL write uses `INSERT ... ON CONFLICT DO UPDATE`. This makes the pipeline **idempotent**: running it twice on the same day produces the same result, and failed runs can be safely retried.

### Audit trail

Every ETL run is logged in `etl_run_log` with status, timing, and record counts. This makes debugging data issues much easier and is a production-grade practice often missing in intern projects.

---

## Database Schema

```
products
├── id, slug (unique), name, description
├── benchmark, currency, inception_date
└── is_active

constituent_etfs                    ← what the product is made of
├── product_id → products
├── ticker, name, weight (0-1)
├── asset_class, region
└── UNIQUE(product_id, ticker)

nav_history                         ← daily NAV time-series
├── product_id → products
├── date, nav (18,6 precision)
├── day_return (%)
└── UNIQUE(product_id, date)

performance_metrics                 ← pre-computed trailing returns
├── product_id → products
├── as_of_date
├── return_1m/3m/6m/ytd/1y/3y/inception (%)
├── volatility_1y, sharpe_ratio, max_drawdown
└── UNIQUE(product_id, as_of_date)

holdings                            ← top-N stock holdings
├── product_id, as_of_date, rank
├── ticker, name, sector, country
├── weight (%), market_cap (USD)
└── UNIQUE(product_id, as_of_date, rank)

sector_exposure                     ← GICS sector breakdown
country_exposure                    ← country + region breakdown
market_cap_exposure                 ← large/mid/small cap breakdown

etl_run_log                         ← audit trail
├── run_type, status, triggered_by
├── started_at, finished_at
├── records_upserted, error_message
```

**Design notes:**
- `NUMERIC` types are used for all financial values — never `FLOAT` — to avoid floating-point rounding errors.
- Exposure tables use `as_of_date` so historical snapshots are preserved (useful for compliance and back-testing).
- Indexes on `(product_id, date DESC)` support the common "latest data for product" query pattern efficiently.

---

## API Reference

Base URL: `http://localhost:8080`

### `GET /api/v1/products`
List all active products.

**Response:**
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "slug": "global-growth-prisma",
      "name": "Prisma Global Growth",
      "benchmark": "MSCI ACWI",
      "currency": "USD",
      "inception_date": "2022-01-03T00:00:00Z"
    }
  ]
}
```

---

### `GET /api/v1/products/{slug}/factsheet`
Full factsheet — the primary endpoint for the product page.

```json
{
  "success": true,
  "data": {
    "product": { "slug": "global-growth-prisma", ... },
    "as_of_date": "2025-05-23",
    "current_nav": 142.38,
    "performance": {
      "return_1m": 2.14,
      "return_3m": 6.82,
      "return_1y": 18.45,
      "volatility_1y": 14.2,
      "sharpe_ratio": 1.12,
      "max_drawdown": -8.3
    },
    "top_holdings": [
      { "rank": 1, "ticker": "AAPL", "name": "Apple Inc.", "weight": 5.8, "sector": "Technology" }
    ],
    "sector_exposure": [
      { "sector": "Technology", "weight": 31.2 }
    ],
    "country_exposure": [
      { "country": "United States", "region": "North America", "weight": 62.1 }
    ],
    "market_cap_exposure": [
      { "cap_bucket": "Large Cap", "weight": 78.4 }
    ],
    "constituents": [
      { "ticker": "VTI", "name": "Vanguard Total Stock Market ETF", "weight": 0.35 }
    ],
    "nav_history": [
      { "date": "2024-05-23", "nav": 118.42, "day_return": 0.34 }
    ]
  }
}
```

---

### `GET /api/v1/products/{slug}/nav?days=365`
NAV time-series for chart rendering. `days` defaults to 365.

### `GET /api/v1/products/{slug}/holdings?limit=10`
Top holdings. `limit` max is 50.

### `GET /api/v1/products/{slug}/exposures`
Sector, country, and market-cap exposure breakdowns.

### `POST /api/v1/admin/sync`
Trigger a manual ETL run.

```json
// Request
{ "product_slug": "global-growth-prisma", "run_type": "full" }

// Response
{ "success": true, "data": { "log_id": 42, "run_type": "full" } }
```

### `GET /api/v1/health`
Health check — returns DB connectivity status.

---

## ETL Pipeline

### Data Sources

| Data | Source | Method |
|------|--------|--------|
| Portfolio NAV | Yahoo Finance (via `yfinance`) | Weighted price history of constituent ETFs |
| Performance metrics | Computed from NAV history | Trailing returns, Sharpe, volatility, drawdown |
| Top Holdings | `yfinance` `funds_data` + curated fallback | ETF holdings aggregation |
| Sector exposure | `yfinance` `sector_weightings` + fallback | Weighted across ETFs |
| Country exposure | Curated factsheet data | Weighted across ETFs |
| Market cap buckets | Curated factsheet data | Weighted across ETFs |

### Constituent ETFs (Prisma Global Growth)

| ETF | Name | Weight | Role |
|-----|------|--------|------|
| VTI | Vanguard Total Stock Market | 35% | US broad market |
| VXUS | Vanguard Total International | 25% | Developed international |
| QQQ | Invesco Nasdaq-100 | 20% | US growth / tech |
| VWO | Vanguard Emerging Markets | 10% | Emerging market exposure |
| GLD | SPDR Gold Shares | 5% | Inflation hedge |
| VNQ | Vanguard Real Estate | 5% | Real asset diversification |

### Schedule

The ETL runs **daily at 18:30** on weekdays, after US market data has settled (US markets close at 4 PM ET / 1:30 AM IST; data is available on Yahoo Finance within ~2 hours).

### Running the ETL manually

```bash
# Full pipeline for all products
python etl/pipeline.py --run-now

# NAV only for a specific product
python etl/pipeline.py --run-now --product global-growth-prisma --run-type nav
```

---

## Setup & Running Locally

### Prerequisites

- Go 1.22+
- Python 3.12+
- Docker & Docker Compose (recommended)

---

### Option A: Docker Compose (recommended)

```bash
# 1. Clone the repository
git clone https://github.com/YOUR_USERNAME/prisma-backend.git
cd prisma-backend

# 2. Start all services (Postgres, API, ETL)
docker compose up --build

# 3. Run the ETL pipeline immediately to populate data
docker compose exec etl python pipeline.py --run-now

# 4. Test the API
curl http://localhost:8080/api/v1/products
curl http://localhost:8080/api/v1/products/global-growth-prisma/factsheet
```

---

### Option B: Manual setup

**1. Start PostgreSQL**
```bash
# Using Docker for just the database
docker run -d \
  --name prisma_db \
  -e POSTGRES_USER=prisma \
  -e POSTGRES_PASSWORD=prisma \
  -e POSTGRES_DB=prismadb \
  -p 5432:5432 \
  postgres:16-alpine
```

**2. Configure environment**
```bash
cp .env.example .env
# Edit .env with your values if needed
```

**3. Run the Go API**
```bash
go mod download
go run ./cmd/server
# Server starts on :8080 and auto-runs migrations
```

**4. Set up Python ETL**
```bash
cd etl
python -m venv venv
source venv/bin/activate          # Windows: venv\Scripts\activate
pip install -r requirements.txt

# Populate data immediately
python pipeline.py --run-now
```

**5. Verify**
```bash
curl http://localhost:8080/api/v1/health
curl http://localhost:8080/api/v1/products/global-growth-prisma/factsheet | jq .
```

---

## Project Structure

```
prisma-backend/
├── cmd/server/
│   └── main.go                  # Server entrypoint, graceful shutdown
├── internal/
│   ├── api/
│   │   ├── router.go            # Chi router, middleware, CORS
│   │   └── handlers/
│   │       ├── factsheet.go     # Product & factsheet endpoints
│   │       └── admin.go         # Sync trigger, health check
│   ├── config/
│   │   └── config.go            # Env-based config loader
│   ├── db/
│   │   ├── db.go                # pgx pool factory, migration runner
│   │   └── migrations/
│   │       └── 001_init.sql     # Full schema + seed data
│   └── models/
│       └── models.go            # Domain structs used by API & DB layers
├── etl/
│   ├── pipeline.py              # Orchestrator + scheduler
│   ├── fetchers/
│   │   ├── yahoo_finance.py     # yfinance wrapper (price history, NAV)
│   │   └── exposure_calculator.py  # Sector/country/mcap aggregation
│   ├── requirements.txt
│   └── Dockerfile.etl
├── docs/
│   └── architecture.md          # Extended architecture notes
├── docker-compose.yml
├── Dockerfile
├── go.mod
├── .env.example
└── README.md
```

---

## Data Freshness Strategy

| Data Type | Update Frequency | Rationale |
|-----------|-----------------|-----------|
| NAV history | Daily (weekdays) | Markets close once a day; intraday NAV not needed for factsheets |
| Performance metrics | Daily | Derived from NAV — recomputed each run |
| Holdings | Daily | ETF holdings update daily after market close |
| Sector/Country exposure | Daily | Derived from holdings weights |
| Market cap buckets | Daily | Derived from holdings |

### Handling data gaps

- ETF price data may be missing for non-overlapping market holidays (e.g. US vs Japan). The pipeline **forward-fills** prices across a shared date index, which is standard practice for multi-asset portfolios.
- If a Yahoo Finance API call fails, the pipeline logs the error to `etl_run_log` with `status='failed'` and continues with other products. The last successful data remains in the DB and is served to the API — stale but available.

---

*Built with Go 1.22, Python 3.12, PostgreSQL 16, yfinance, pgx/v5, chi.*
