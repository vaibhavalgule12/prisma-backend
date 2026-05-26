-- =============================================================
-- Spring Street – Prisma Factsheet Backend
-- Schema: 001_init.sql
-- =============================================================

-- ---------------------------------------------------------------
-- PRODUCTS  (one row per investable product, e.g. "global-growth-prisma")
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS products (
    id              SERIAL PRIMARY KEY,
    slug            VARCHAR(100) UNIQUE NOT NULL,   -- URL-safe identifier
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    benchmark       VARCHAR(100),                   -- e.g. "MSCI ACWI"
    currency        CHAR(3) NOT NULL DEFAULT 'USD',
    inception_date  DATE NOT NULL,
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------
-- CONSTITUENT_ETFS  (the underlying ETFs that make up a product)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS constituent_etfs (
    id          SERIAL PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    ticker      VARCHAR(20) NOT NULL,
    name        VARCHAR(255),
    weight      NUMERIC(6,4) NOT NULL,     -- 0.35 = 35%
    asset_class VARCHAR(50),               -- 'equity', 'fixed_income', 'commodity'
    region      VARCHAR(50),               -- 'us', 'international', 'emerging'
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, ticker)
);

-- ---------------------------------------------------------------
-- NAV_HISTORY  (daily Net Asset Value / portfolio value per unit)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS nav_history (
    id          BIGSERIAL PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    date        DATE NOT NULL,
    nav         NUMERIC(18,6) NOT NULL,       -- portfolio NAV in USD
    day_return  NUMERIC(10,6),                -- daily return %
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, date)
);

CREATE INDEX IF NOT EXISTS idx_nav_history_product_date
    ON nav_history(product_id, date DESC);

-- ---------------------------------------------------------------
-- PERFORMANCE_METRICS  (pre-computed trailing returns)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS performance_metrics (
    id              SERIAL PRIMARY KEY,
    product_id      INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    as_of_date      DATE NOT NULL,
    return_1m       NUMERIC(10,4),   -- % returns
    return_3m       NUMERIC(10,4),
    return_6m       NUMERIC(10,4),
    return_ytd      NUMERIC(10,4),
    return_1y       NUMERIC(10,4),
    return_3y       NUMERIC(10,4),
    return_inception NUMERIC(10,4),
    volatility_1y   NUMERIC(10,4),   -- annualised std dev %
    sharpe_ratio    NUMERIC(8,4),
    max_drawdown    NUMERIC(10,4),   -- %
    benchmark_return_1y NUMERIC(10,4),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, as_of_date)
);

-- ---------------------------------------------------------------
-- HOLDINGS  (top-N individual stock holdings, aggregated across ETFs)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS holdings (
    id          BIGSERIAL PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    as_of_date  DATE NOT NULL,
    rank        SMALLINT NOT NULL,
    ticker      VARCHAR(20) NOT NULL,
    name        VARCHAR(255),
    sector      VARCHAR(100),
    country     VARCHAR(100),
    weight      NUMERIC(8,4) NOT NULL,   -- % of total portfolio
    market_cap  BIGINT,                  -- in USD
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, as_of_date, rank)
);

CREATE INDEX IF NOT EXISTS idx_holdings_product_date
    ON holdings(product_id, as_of_date DESC);

-- ---------------------------------------------------------------
-- SECTOR_EXPOSURE  (aggregated sector breakdown)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sector_exposure (
    id          BIGSERIAL PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    as_of_date  DATE NOT NULL,
    sector      VARCHAR(100) NOT NULL,
    weight      NUMERIC(8,4) NOT NULL,    -- % of portfolio
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, as_of_date, sector)
);

-- ---------------------------------------------------------------
-- COUNTRY_EXPOSURE  (aggregated country / region breakdown)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS country_exposure (
    id          BIGSERIAL PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    as_of_date  DATE NOT NULL,
    country     VARCHAR(100) NOT NULL,
    region      VARCHAR(100),             -- 'North America', 'Europe', etc.
    weight      NUMERIC(8,4) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, as_of_date, country)
);

-- ---------------------------------------------------------------
-- MARKET_CAP_EXPOSURE  (large / mid / small cap breakdown)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS market_cap_exposure (
    id          BIGSERIAL PRIMARY KEY,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    as_of_date  DATE NOT NULL,
    cap_bucket  VARCHAR(50) NOT NULL,    -- 'Large Cap', 'Mid Cap', 'Small Cap'
    weight      NUMERIC(8,4) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(product_id, as_of_date, cap_bucket)
);

-- ---------------------------------------------------------------
-- ETL_RUN_LOG  (audit trail for every pipeline execution)
-- ---------------------------------------------------------------
CREATE TABLE IF NOT EXISTS etl_run_log (
    id              BIGSERIAL PRIMARY KEY,
    product_id      INTEGER REFERENCES products(id),
    run_type        VARCHAR(50) NOT NULL,   -- 'nav', 'holdings', 'exposures', 'full'
    status          VARCHAR(20) NOT NULL,   -- 'running', 'success', 'failed'
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at     TIMESTAMPTZ,
    records_upserted INTEGER,
    error_message   TEXT,
    triggered_by    VARCHAR(50)             -- 'scheduler', 'manual', 'api'
);

-- ---------------------------------------------------------------
-- SEED: Insert the Prisma Global Growth product
-- ---------------------------------------------------------------
INSERT INTO products (slug, name, description, benchmark, currency, inception_date)
VALUES (
    'global-growth-prisma',
    'Prisma Global Growth',
    'A diversified global equity portfolio designed for long-term capital appreciation, providing Indian investors institutional-grade access to global growth opportunities across developed and emerging markets.',
    'MSCI ACWI',
    'USD',
    '2022-01-03'
) ON CONFLICT (slug) DO NOTHING;

-- Seed constituent ETFs (weights sum to 1.0)
INSERT INTO constituent_etfs (product_id, ticker, name, weight, asset_class, region)
SELECT p.id, v.ticker, v.name, v.weight, v.asset_class, v.region
FROM products p,
(VALUES
    ('VTI',  'Vanguard Total Stock Market ETF',            0.35, 'equity', 'us'),
    ('VXUS', 'Vanguard Total International Stock ETF',     0.25, 'equity', 'international'),
    ('QQQ',  'Invesco QQQ Trust (Nasdaq-100)',             0.20, 'equity', 'us'),
    ('VWO',  'Vanguard FTSE Emerging Markets ETF',        0.10, 'equity', 'emerging'),
    ('GLD',  'SPDR Gold Shares',                          0.05, 'commodity', 'global'),
    ('VNQ',  'Vanguard Real Estate ETF',                  0.05, 'equity', 'us')
) AS v(ticker, name, weight, asset_class, region)
WHERE p.slug = 'global-growth-prisma'
ON CONFLICT (product_id, ticker) DO NOTHING;
