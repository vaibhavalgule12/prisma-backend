"""
etl/pipeline.py

Spring Street – Prisma Factsheet ETL Pipeline
==============================================

Runs as a standalone service (or one-shot via --run-now).
Connects to PostgreSQL, fetches data from Yahoo Finance, and
upserts NAV history, performance metrics, holdings, and exposures.

Usage:
    python pipeline.py                # start scheduler (runs daily at 6:30 PM)
    python pipeline.py --run-now      # run once immediately and exit
    python pipeline.py --run-now --product global-growth-prisma
"""

import argparse
import logging
import os
import sys
import time
from datetime import date, timedelta
from typing import Optional

import numpy as np
import psycopg2
import psycopg2.extras
import schedule
from dotenv import load_dotenv

from fetchers.yahoo_finance import compute_portfolio_nav, fetch_current_price
from fetchers.exposure_calculator import (
    compute_sector_exposure,
    compute_country_exposure,
    compute_market_cap_exposure,
    get_top_holdings,
)

load_dotenv()

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
log = logging.getLogger("prisma.etl")


# ── Database ──────────────────────────────────────────────────────────────────

def get_db_conn():
    return psycopg2.connect(
        host=os.getenv("DB_HOST", "localhost"),
        port=int(os.getenv("DB_PORT", 5432)),
        user=os.getenv("DB_USER", "prisma"),
        password=os.getenv("DB_PASSWORD", "prisma"),
        dbname=os.getenv("DB_NAME", "prismadb"),
    )


# ── ETL steps ─────────────────────────────────────────────────────────────────

def etl_nav(conn, product_id: int, product_slug: str, constituent_weights: dict):
    """Fetch and upsert NAV history for the portfolio."""
    log.info("[%s] Fetching NAV history…", product_slug)

    nav_df = compute_portfolio_nav(constituent_weights, base_nav=100.0)
    if nav_df.empty:
        log.warning("[%s] NAV DataFrame is empty — skipping", product_slug)
        return 0

    records = [
        (product_id, row["date"], round(row["nav"], 6), round(row["day_return"], 6))
        for _, row in nav_df.iterrows()
    ]

    with conn.cursor() as cur:
        psycopg2.extras.execute_values(
            cur,
            """
            INSERT INTO nav_history (product_id, date, nav, day_return)
            VALUES %s
            ON CONFLICT (product_id, date)
            DO UPDATE SET nav = EXCLUDED.nav, day_return = EXCLUDED.day_return
            """,
            records,
        )
    conn.commit()
    log.info("[%s] Upserted %d NAV records", product_slug, len(records))
    return len(records)


def etl_performance(conn, product_id: int, product_slug: str):
    """
    Compute trailing returns from the nav_history table and upsert
    into performance_metrics.
    """
    log.info("[%s] Computing performance metrics…", product_slug)
    as_of = date.today()

    with conn.cursor() as cur:
        # Pull all nav history sorted ascending
        cur.execute(
            "SELECT date, nav FROM nav_history WHERE product_id=%s ORDER BY date ASC",
            (product_id,),
        )
        rows = cur.fetchall()

    if not rows:
        log.warning("[%s] No NAV data for performance calc", product_slug)
        return

    nav_by_date = {r[0]: r[1] for r in rows}
    all_dates = sorted(nav_by_date)
    latest_date = all_dates[-1]
    nav_latest = float(nav_by_date[latest_date])

    def nav_on_or_before(target: date) -> Optional[float]:
        for d in reversed(all_dates):
            if d <= target:
                return float(nav_by_date[d])
        return None

    def trailing_return(days: int) -> Optional[float]:
        start_date = latest_date - timedelta(days=days)
        nav_start = nav_on_or_before(start_date)
        if nav_start and nav_start != 0:
            return round((nav_latest / nav_start - 1) * 100, 4)
        return None

    def ytd_return() -> Optional[float]:
        ytd_start = date(latest_date.year, 1, 1)
        nav_start = nav_on_or_before(ytd_start)
        if nav_start and nav_start != 0:
            return round((nav_latest / nav_start - 1) * 100, 4)
        return None

    def inception_return() -> Optional[float]:
        nav_start = float(nav_by_date[all_dates[0]])
        if nav_start != 0:
            return round((nav_latest / nav_start - 1) * 100, 4)
        return None

    # Annualised volatility (252 trading days)
    navs = [float(nav_by_date[d]) for d in all_dates[-252:]]
    if len(navs) > 2:
        daily_rets = [(navs[i] / navs[i - 1] - 1) for i in range(1, len(navs))]
        vol_1y = round(float(np.std(daily_rets) * np.sqrt(252) * 100), 4)
        mean_ret = np.mean(daily_rets) * 252
        risk_free = 0.053  # approx US 3M T-bill
        sharpe = round(float((mean_ret - risk_free) / (vol_1y / 100 + 1e-9)), 4)
    else:
        vol_1y = None
        sharpe = None

    # Max drawdown over last 252 trading days
    if len(navs) > 1:
        peak = navs[0]
        max_dd = 0.0
        for n in navs:
            peak = max(peak, n)
            dd = (n - peak) / peak * 100
            max_dd = min(max_dd, dd)
        max_drawdown = round(max_dd, 4)
    else:
        max_drawdown = None

    with conn.cursor() as cur:
        cur.execute(
            """
            INSERT INTO performance_metrics (
                product_id, as_of_date,
                return_1m, return_3m, return_6m, return_ytd,
                return_1y, return_3y, return_inception,
                volatility_1y, sharpe_ratio, max_drawdown
            ) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
            ON CONFLICT (product_id, as_of_date)
            DO UPDATE SET
                return_1m=EXCLUDED.return_1m,
                return_3m=EXCLUDED.return_3m,
                return_6m=EXCLUDED.return_6m,
                return_ytd=EXCLUDED.return_ytd,
                return_1y=EXCLUDED.return_1y,
                return_3y=EXCLUDED.return_3y,
                return_inception=EXCLUDED.return_inception,
                volatility_1y=EXCLUDED.volatility_1y,
                sharpe_ratio=EXCLUDED.sharpe_ratio,
                max_drawdown=EXCLUDED.max_drawdown
            """,
            (
                product_id, as_of,
                trailing_return(30),
                trailing_return(90),
                trailing_return(180),
                ytd_return(),
                trailing_return(365),
                trailing_return(365 * 3),
                inception_return(),
                vol_1y,
                sharpe,
                max_drawdown,
            ),
        )
    conn.commit()
    log.info("[%s] Performance metrics updated for %s", product_slug, as_of)


def etl_holdings(conn, product_id: int, product_slug: str, constituent_weights: dict):
    """Upsert top holdings with sector/country annotations."""
    log.info("[%s] Fetching holdings…", product_slug)
    holdings = get_top_holdings(constituent_weights, top_n=10)

    # Enrich with market cap from Yahoo Finance
    as_of = date.today()
    records = []
    for rank, h in enumerate(holdings, start=1):
        try:
            import yfinance as yf
            info = yf.Ticker(h["ticker"]).fast_info
            mcap = int(info.market_cap) if hasattr(info, "market_cap") and info.market_cap else None
        except Exception:
            mcap = None

        records.append((
            product_id, as_of, rank,
            h["ticker"], h["name"], h["sector"], h["country"],
            round(h["weight"], 4), mcap,
        ))

    with conn.cursor() as cur:
        psycopg2.extras.execute_values(
            cur,
            """
            INSERT INTO holdings
                (product_id, as_of_date, rank, ticker, name, sector, country, weight, market_cap)
            VALUES %s
            ON CONFLICT (product_id, as_of_date, rank)
            DO UPDATE SET
                ticker=EXCLUDED.ticker, name=EXCLUDED.name,
                sector=EXCLUDED.sector, country=EXCLUDED.country,
                weight=EXCLUDED.weight, market_cap=EXCLUDED.market_cap
            """,
            records,
        )
    conn.commit()
    log.info("[%s] Upserted %d holdings", product_slug, len(records))
    return len(records)


def etl_exposures(conn, product_id: int, product_slug: str, constituent_weights: dict):
    """Compute and upsert sector, country, and market-cap exposures."""
    log.info("[%s] Computing exposures…", product_slug)
    as_of = date.today()

    sectors = compute_sector_exposure(constituent_weights)
    countries = compute_country_exposure(constituent_weights)
    mcaps = compute_market_cap_exposure(constituent_weights)

    with conn.cursor() as cur:
        # Sector
        psycopg2.extras.execute_values(
            cur,
            """
            INSERT INTO sector_exposure (product_id, as_of_date, sector, weight)
            VALUES %s
            ON CONFLICT (product_id, as_of_date, sector)
            DO UPDATE SET weight=EXCLUDED.weight
            """,
            [(product_id, as_of, s["sector"], s["weight"]) for s in sectors],
        )

        # Country
        psycopg2.extras.execute_values(
            cur,
            """
            INSERT INTO country_exposure (product_id, as_of_date, country, region, weight)
            VALUES %s
            ON CONFLICT (product_id, as_of_date, country)
            DO UPDATE SET region=EXCLUDED.region, weight=EXCLUDED.weight
            """,
            [(product_id, as_of, c["country"], c["region"], c["weight"]) for c in countries],
        )

        # Market cap
        psycopg2.extras.execute_values(
            cur,
            """
            INSERT INTO market_cap_exposure (product_id, as_of_date, cap_bucket, weight)
            VALUES %s
            ON CONFLICT (product_id, as_of_date, cap_bucket)
            DO UPDATE SET weight=EXCLUDED.weight
            """,
            [(product_id, as_of, m["cap_bucket"], m["weight"]) for m in mcaps],
        )

    conn.commit()
    log.info("[%s] Exposures upserted (%d sectors, %d countries, %d mcap buckets)",
             product_slug, len(sectors), len(countries), len(mcaps))


# ── Orchestrator ──────────────────────────────────────────────────────────────

def run_pipeline(product_slug: Optional[str] = None, run_type: str = "full"):
    """Run the full or partial ETL pipeline for one or all products."""
    log.info("Pipeline started | run_type=%s product=%s", run_type, product_slug or "all")

    try:
        conn = get_db_conn()
    except Exception as exc:
        log.error("DB connection failed: %s", exc)
        return

    try:
        with conn.cursor() as cur:
            if product_slug:
                cur.execute(
                    "SELECT id, slug FROM products WHERE slug=%s AND is_active=true",
                    (product_slug,),
                )
            else:
                cur.execute("SELECT id, slug FROM products WHERE is_active=true")
            products = cur.fetchall()

        if not products:
            log.warning("No products found")
            return

        for product_id, slug in products:
            # Get constituent ETFs and weights
            with conn.cursor() as cur:
                cur.execute(
                    "SELECT ticker, weight FROM constituent_etfs WHERE product_id=%s",
                    (product_id,),
                )
                rows = cur.fetchall()
            constituent_weights = {row[0]: float(row[1]) for row in rows}

            log_id = _log_start(conn, product_id, run_type)
            records = 0
            try:
                if run_type in ("full", "nav"):
                    records += etl_nav(conn, product_id, slug, constituent_weights)
                if run_type in ("full", "performance"):
                    etl_performance(conn, product_id, slug)
                if run_type in ("full", "holdings"):
                    records += etl_holdings(conn, product_id, slug, constituent_weights)
                if run_type in ("full", "exposures"):
                    etl_exposures(conn, product_id, slug, constituent_weights)
                _log_finish(conn, log_id, "success", records)
                log.info("[%s] Pipeline completed successfully", slug)
            except Exception as exc:
                _log_finish(conn, log_id, "failed", 0, str(exc))
                log.error("[%s] Pipeline error: %s", slug, exc)

    finally:
        conn.close()


def _log_start(conn, product_id: int, run_type: str) -> int:
    with conn.cursor() as cur:
        cur.execute(
            """INSERT INTO etl_run_log (product_id, run_type, status, triggered_by)
               VALUES (%s,%s,'running','scheduler') RETURNING id""",
            (product_id, run_type),
        )
        log_id = cur.fetchone()[0]
    conn.commit()
    return log_id


def _log_finish(conn, log_id: int, status: str, records: int, error: str = None):
    with conn.cursor() as cur:
        cur.execute(
            """UPDATE etl_run_log
               SET status=%s, finished_at=NOW(), records_upserted=%s, error_message=%s
               WHERE id=%s""",
            (status, records, error, log_id),
        )
    conn.commit()


# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Prisma ETL Pipeline")
    parser.add_argument("--run-now", action="store_true", help="Run immediately and exit")
    parser.add_argument("--product", type=str, default=None, help="Product slug to run for")
    parser.add_argument("--run-type", type=str, default="full",
                        choices=["full", "nav", "performance", "holdings", "exposures"],
                        help="Which ETL step to run")
    args = parser.parse_args()

    if args.run_now:
        run_pipeline(product_slug=args.product, run_type=args.run_type)
        sys.exit(0)

    # Scheduled mode: run every weekday at 18:30 IST (after US market data settles)
    log.info("Starting scheduler — will run daily at 18:30 on weekdays")
    schedule.every().monday.at("18:30").do(run_pipeline)
    schedule.every().tuesday.at("18:30").do(run_pipeline)
    schedule.every().wednesday.at("18:30").do(run_pipeline)
    schedule.every().thursday.at("18:30").do(run_pipeline)
    schedule.every().friday.at("18:30").do(run_pipeline)

    while True:
        schedule.run_pending()
        time.sleep(60)
