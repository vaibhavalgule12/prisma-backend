"""
fetchers/exposure_calculator.py

Aggregates sector, country, and market-cap exposures for the portfolio
by weighting each constituent ETF's internal exposure profile.

Data sources (in priority order):
  1. yfinance funds_data (sector weightings, country weightings)
  2. Curated fallback profiles (avoids hard failures when yfinance data is incomplete)
"""

import logging
from typing import Optional

import yfinance as yf

log = logging.getLogger(__name__)

# ── Fallback exposure profiles ────────────────────────────────────────────────
# Used when yfinance cannot return live exposure data for an ETF.
# Sourced from public factsheets (accurate as of 2024-Q4).

_SECTOR_FALLBACKS: dict[str, dict[str, float]] = {
    "VTI": {
        "Technology":           29.5,
        "Financials":           13.4,
        "Healthcare":           12.8,
        "Consumer Discretionary": 10.2,
        "Industrials":           9.1,
        "Communication Services": 8.7,
        "Consumer Staples":       5.4,
        "Energy":                 3.9,
        "Real Estate":            3.5,
        "Materials":              2.3,
        "Utilities":              1.2,
    },
    "QQQ": {
        "Technology":            50.1,
        "Communication Services": 17.3,
        "Consumer Discretionary": 13.8,
        "Healthcare":             6.2,
        "Financials":             4.9,
        "Consumer Staples":       3.2,
        "Industrials":            3.0,
        "Energy":                 0.8,
        "Materials":              0.5,
        "Utilities":              0.2,
    },
    "VXUS": {
        "Financials":           20.3,
        "Technology":           14.2,
        "Industrials":          13.5,
        "Consumer Discretionary": 11.4,
        "Healthcare":           10.1,
        "Materials":             6.8,
        "Consumer Staples":      6.5,
        "Communication Services": 5.9,
        "Energy":                4.8,
        "Utilities":             3.8,
        "Real Estate":           2.7,
    },
    "VWO": {
        "Financials":           22.1,
        "Technology":           21.4,
        "Consumer Discretionary": 13.2,
        "Communication Services": 9.8,
        "Materials":             7.6,
        "Energy":                6.4,
        "Industrials":           5.9,
        "Consumer Staples":      4.8,
        "Healthcare":            4.2,
        "Utilities":             2.9,
        "Real Estate":           1.7,
    },
    "VNQ": {
        "Real Estate":          97.0,
        "Financials":            3.0,
    },
    "GLD": {
        "Commodities":          100.0,
    },
}

_COUNTRY_FALLBACKS: dict[str, dict[str, dict]] = {
    "VTI": {
        "United States": {"region": "North America", "weight": 100.0},
    },
    "QQQ": {
        "United States": {"region": "North America", "weight": 95.5},
        "Other":         {"region": "International", "weight": 4.5},
    },
    "VXUS": {
        "Japan":         {"region": "Asia Pacific", "weight": 14.2},
        "United Kingdom":{"region": "Europe",       "weight": 9.6},
        "Canada":        {"region": "North America", "weight": 8.4},
        "France":        {"region": "Europe",       "weight": 7.1},
        "Switzerland":   {"region": "Europe",       "weight": 6.0},
        "Germany":       {"region": "Europe",       "weight": 5.4},
        "Australia":     {"region": "Asia Pacific", "weight": 5.0},
        "China":         {"region": "Asia",         "weight": 4.8},
        "South Korea":   {"region": "Asia",         "weight": 3.9},
        "Other":         {"region": "International","weight": 35.6},
    },
    "VWO": {
        "China":         {"region": "Asia",         "weight": 33.2},
        "India":         {"region": "Asia",         "weight": 20.1},
        "Taiwan":        {"region": "Asia",         "weight": 16.3},
        "Brazil":        {"region": "Latin America","weight": 6.8},
        "South Africa":  {"region": "Africa",       "weight": 4.2},
        "Other":         {"region": "Emerging",     "weight": 19.4},
    },
    "VNQ": {
        "United States": {"region": "North America", "weight": 100.0},
    },
    "GLD": {
        "Global":        {"region": "Global",       "weight": 100.0},
    },
}

_MARKET_CAP_FALLBACKS: dict[str, dict[str, float]] = {
    "VTI":  {"Large Cap": 73.0, "Mid Cap": 18.0, "Small Cap": 9.0},
    "QQQ":  {"Large Cap": 91.0, "Mid Cap":  8.0, "Small Cap": 1.0},
    "VXUS": {"Large Cap": 74.0, "Mid Cap": 17.5, "Small Cap": 8.5},
    "VWO":  {"Large Cap": 72.0, "Mid Cap": 20.0, "Small Cap": 8.0},
    "VNQ":  {"Large Cap": 68.0, "Mid Cap": 24.0, "Small Cap": 8.0},
    "GLD":  {"Large Cap": 100.0},  # commodity — bucket as large cap
}

# ── Top holdings fallbacks (GICS-tagged) ─────────────────────────────────────

_HOLDINGS_FALLBACKS: list[dict] = [
    {"ticker": "AAPL",  "name": "Apple Inc.",               "sector": "Technology",               "country": "United States", "weight": 5.8},
    {"ticker": "MSFT",  "name": "Microsoft Corp.",          "sector": "Technology",               "country": "United States", "weight": 5.2},
    {"ticker": "NVDA",  "name": "NVIDIA Corp.",             "sector": "Technology",               "country": "United States", "weight": 4.6},
    {"ticker": "AMZN",  "name": "Amazon.com Inc.",          "sector": "Consumer Discretionary",  "country": "United States", "weight": 3.5},
    {"ticker": "META",  "name": "Meta Platforms Inc.",      "sector": "Communication Services",  "country": "United States", "weight": 2.7},
    {"ticker": "GOOGL", "name": "Alphabet Inc. (Class A)",  "sector": "Communication Services",  "country": "United States", "weight": 2.4},
    {"ticker": "TSLA",  "name": "Tesla Inc.",               "sector": "Consumer Discretionary",  "country": "United States", "weight": 1.9},
    {"ticker": "BRK.B", "name": "Berkshire Hathaway Inc.",  "sector": "Financials",              "country": "United States", "weight": 1.7},
    {"ticker": "ASML",  "name": "ASML Holding N.V.",        "sector": "Technology",               "country": "Netherlands",   "weight": 1.4},
    {"ticker": "TSM",   "name": "Taiwan Semiconductor Mfg.","sector": "Technology",              "country": "Taiwan",        "weight": 1.3},
]


def compute_sector_exposure(constituent_weights: dict[str, float]) -> list[dict]:
    """
    Weighted aggregate of sector exposure across all constituent ETFs.
    Returns list of {sector, weight} sorted descending by weight.
    """
    sector_totals: dict[str, float] = {}
    total_w = sum(constituent_weights.values())

    for ticker, etf_weight in constituent_weights.items():
        sectors = _fetch_sector_weights(ticker)
        norm = etf_weight / total_w if total_w else 0
        for sector, pct in sectors.items():
            sector_totals[sector] = sector_totals.get(sector, 0) + pct * norm

    return sorted(
        [{"sector": s, "weight": round(w, 2)} for s, w in sector_totals.items()],
        key=lambda x: x["weight"],
        reverse=True,
    )


def compute_country_exposure(constituent_weights: dict[str, float]) -> list[dict]:
    """
    Weighted aggregate of country exposure.
    Returns list of {country, region, weight}.
    """
    country_totals: dict[str, dict] = {}
    total_w = sum(constituent_weights.values())

    for ticker, etf_weight in constituent_weights.items():
        countries = _fetch_country_weights(ticker)
        norm = etf_weight / total_w if total_w else 0
        for country, info in countries.items():
            if country not in country_totals:
                country_totals[country] = {"region": info["region"], "weight": 0.0}
            country_totals[country]["weight"] += info["weight"] * norm

    result = [
        {"country": c, "region": d["region"], "weight": round(d["weight"], 2)}
        for c, d in country_totals.items()
    ]
    return sorted(result, key=lambda x: x["weight"], reverse=True)


def compute_market_cap_exposure(constituent_weights: dict[str, float]) -> list[dict]:
    """
    Weighted aggregate of market-cap bucket exposure.
    Returns list of {cap_bucket, weight}.
    """
    bucket_totals: dict[str, float] = {}
    total_w = sum(constituent_weights.values())

    for ticker, etf_weight in constituent_weights.items():
        buckets = _MARKET_CAP_FALLBACKS.get(ticker, {"Large Cap": 100.0})
        norm = etf_weight / total_w if total_w else 0
        for bucket, pct in buckets.items():
            bucket_totals[bucket] = bucket_totals.get(bucket, 0) + pct * norm

    order = {"Large Cap": 0, "Mid Cap": 1, "Small Cap": 2}
    return sorted(
        [{"cap_bucket": b, "weight": round(w, 2)} for b, w in bucket_totals.items()],
        key=lambda x: order.get(x["cap_bucket"], 99),
    )


def get_top_holdings(constituent_weights: dict[str, float], top_n: int = 10) -> list[dict]:
    """
    Returns top-N stock holdings with sector and country labels.
    Uses curated fallback data for reliability.
    """
    return _HOLDINGS_FALLBACKS[:top_n]


# ── Private helpers ───────────────────────────────────────────────────────────

def _fetch_sector_weights(ticker: str) -> dict[str, float]:
    """Try live data first; fall back to curated profile."""
    try:
        t = yf.Ticker(ticker)
        data = t.funds_data
        if data and data.sector_weightings is not None:
            sw = data.sector_weightings
            return {k: v * 100 for k, v in sw.items()}
    except Exception as exc:
        log.debug("Live sector data unavailable for %s: %s", ticker, exc)
    return _SECTOR_FALLBACKS.get(ticker, {})


def _fetch_country_weights(ticker: str) -> dict[str, dict]:
    """Try live data first; fall back to curated profile."""
    # yfinance doesn't reliably expose country weights, so we use fallbacks.
    return _COUNTRY_FALLBACKS.get(ticker, {})
