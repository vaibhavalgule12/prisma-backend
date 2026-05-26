"""
fetchers/yahoo_finance.py

Fetches market data (price history, info, fast_info) from Yahoo Finance
via the yfinance library. Keeps all network I/O isolated here so the
rest of the pipeline never touches yfinance directly.
"""

import logging
from datetime import date, timedelta
from typing import Optional

import numpy as np
import pandas as pd
import yfinance as yf

log = logging.getLogger(__name__)


def fetch_price_history(
    ticker: str,
    start: Optional[date] = None,
    end: Optional[date] = None,
    period: str = "2y",
) -> pd.DataFrame:
    """
    Returns a DataFrame with columns: [Date, Open, High, Low, Close, Volume]
    sorted ascending by Date.

    Prefer `start`/`end` for precise ranges; fall back to `period`.
    """
    t = yf.Ticker(ticker)
    if start:
        hist = t.history(start=start.isoformat(), end=(end or date.today()).isoformat())
    else:
        hist = t.history(period=period)

    if hist.empty:
        log.warning("No price history for %s", ticker)
        return pd.DataFrame()

    hist = hist.reset_index()
    hist["Date"] = pd.to_datetime(hist["Date"]).dt.date
    return hist[["Date", "Open", "High", "Low", "Close", "Volume"]].sort_values("Date")


def fetch_current_price(ticker: str) -> Optional[float]:
    """Returns the most recent closing price."""
    t = yf.Ticker(ticker)
    try:
        info = t.fast_info
        return float(info.last_price)
    except Exception as exc:
        log.error("fetch_current_price(%s): %s", ticker, exc)
        return None


def fetch_etf_info(ticker: str) -> dict:
    """Returns a dict of metadata for the ETF (name, category, etc.)."""
    try:
        t = yf.Ticker(ticker)
        info = t.info
        return {
            "ticker": ticker,
            "name": info.get("longName") or info.get("shortName", ticker),
            "category": info.get("category", ""),
            "fund_family": info.get("fundFamily", ""),
            "total_assets": info.get("totalAssets"),
            "nav": info.get("navPrice") or info.get("regularMarketPrice"),
        }
    except Exception as exc:
        log.error("fetch_etf_info(%s): %s", ticker, exc)
        return {"ticker": ticker}


def fetch_top_holdings(ticker: str) -> list[dict]:
    """
    Returns the top holdings for an ETF ticker.
    Falls back to an empty list if the data is unavailable.
    """
    try:
        t = yf.Ticker(ticker)
        holdings = t.funds_data.top_holdings
        if holdings is None or holdings.empty:
            return []
        # Normalise columns
        holdings = holdings.reset_index()
        result = []
        for _, row in holdings.iterrows():
            result.append({
                "ticker": row.get("Symbol") or row.get("symbol", ""),
                "name": row.get("Name") or row.get("name") or row.get("Holding Name", ""),
                "weight": float(row.get("% Assets") or row.get("holdingPercent", 0)),
            })
        return result
    except Exception as exc:
        log.warning("fetch_top_holdings(%s): %s — using fallback data", ticker, exc)
        return []


def compute_portfolio_nav(
    constituent_weights: dict[str, float],
    base_nav: float = 100.0,
    start_date: Optional[date] = None,
) -> pd.DataFrame:
    """
    Computes the blended NAV series for the portfolio from the individual
    constituent price histories.

    Args:
        constituent_weights: {ticker: weight}  (weights must sum to ~1.0)
        base_nav: starting NAV value
        start_date: history start (defaults to 2 years ago)

    Returns:
        DataFrame with columns [date, nav, day_return]
    """
    if not start_date:
        start_date = date.today() - timedelta(days=730)

    prices: dict[str, pd.Series] = {}
    for ticker in constituent_weights:
        hist = fetch_price_history(ticker, start=start_date)
        if not hist.empty:
            prices[ticker] = hist.set_index("Date")["Close"]

    if not prices:
        log.error("No price data fetched for any constituent")
        return pd.DataFrame()

    # Align on common trading dates
    price_df = pd.DataFrame(prices).dropna(how="all")

    # Forward-fill stale prices (e.g. different market holidays)
    price_df = price_df.ffill().bfill()

    # Compute daily portfolio return as weighted average of constituent returns
    daily_returns = price_df.pct_change().fillna(0)
    portfolio_return = sum(
        daily_returns[ticker] * w
        for ticker, w in constituent_weights.items()
        if ticker in daily_returns.columns
    )

    # Cumulative NAV starting at base_nav
    nav_series = (1 + portfolio_return).cumprod() * base_nav

    result = pd.DataFrame({
        "date": nav_series.index,
        "nav": nav_series.values,
        "day_return": portfolio_return.values * 100,  # as %
    })
    return result
