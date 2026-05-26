package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/springstreet/prisma-backend/internal/models"
)

type FactsheetHandler struct {
	db  *pgxpool.Pool
	log *slog.Logger
}

func NewFactsheetHandler(db *pgxpool.Pool, log *slog.Logger) *FactsheetHandler {
	return &FactsheetHandler{db: db, log: log}
}

// GetFactsheet returns the full product factsheet in one call.
// GET /api/v1/products/{slug}/factsheet
func (h *FactsheetHandler) GetFactsheet(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	ctx := r.Context()

	product, err := h.fetchProduct(ctx, slug)
	if err != nil {
		if err == pgx.ErrNoRows {
			respondError(w, http.StatusNotFound, "product not found")
			return
		}
		h.log.Error("fetch product", "slug", slug, "err", err)
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	asOfDate, nav, err := h.fetchLatestNAV(ctx, product.ID)
	if err != nil && err != pgx.ErrNoRows {
		h.log.Error("fetch nav", "err", err)
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	performance, _ := h.fetchPerformance(ctx, product.ID, asOfDate)
	topHoldings, _ := h.fetchHoldings(ctx, product.ID, asOfDate, 10)
	sectorExp, _ := h.fetchSectorExposure(ctx, product.ID, asOfDate)
	countryExp, _ := h.fetchCountryExposure(ctx, product.ID, asOfDate)
	mcapExp, _ := h.fetchMarketCapExposure(ctx, product.ID, asOfDate)
	constituents, _ := h.fetchConstituents(ctx, product.ID)
	navHistory, _ := h.fetchNAVHistory(ctx, product.ID, 365)

	fs := models.Factsheet{
		Product:           product,
		AsOfDate:          asOfDate,
		CurrentNAV:        nav,
		Performance:       performance,
		TopHoldings:       topHoldings,
		SectorExposure:    sectorExp,
		CountryExposure:   countryExp,
		MarketCapExposure: mcapExp,
		Constituents:      constituents,
		NAVHistory:        navHistory,
	}

	respondOK(w, fs)
}

// ListProducts returns all active products.
// GET /api/v1/products
func (h *FactsheetHandler) ListProducts(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT id, slug, name, description, benchmark, currency, inception_date
		FROM products WHERE is_active = true ORDER BY name
	`)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description,
			&p.Benchmark, &p.Currency, &p.InceptionDate); err != nil {
			continue
		}
		products = append(products, p)
	}
	respondOK(w, products)
}

// GetNAVHistory returns NAV time-series for chart rendering.
// GET /api/v1/products/{slug}/nav?days=365
func (h *FactsheetHandler) GetNAVHistory(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	days := 365
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 {
			days = parsed
		}
	}

	product, err := h.fetchProduct(r.Context(), slug)
	if err != nil {
		respondError(w, http.StatusNotFound, "product not found")
		return
	}

	navHistory, err := h.fetchNAVHistory(r.Context(), product.ID, days)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondOK(w, navHistory)
}

// GetHoldings returns top-N holdings.
// GET /api/v1/products/{slug}/holdings?limit=10
func (h *FactsheetHandler) GetHoldings(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	product, err := h.fetchProduct(r.Context(), slug)
	if err != nil {
		respondError(w, http.StatusNotFound, "product not found")
		return
	}

	asOfDate, _, _ := h.fetchLatestNAV(r.Context(), product.ID)
	holdings, err := h.fetchHoldings(r.Context(), product.ID, asOfDate, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	respondOK(w, holdings)
}

// GetExposures returns all exposure breakdowns.
// GET /api/v1/products/{slug}/exposures
func (h *FactsheetHandler) GetExposures(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	product, err := h.fetchProduct(r.Context(), slug)
	if err != nil {
		respondError(w, http.StatusNotFound, "product not found")
		return
	}

	asOfDate, _, _ := h.fetchLatestNAV(r.Context(), product.ID)
	sectorExp, _ := h.fetchSectorExposure(r.Context(), product.ID, asOfDate)
	countryExp, _ := h.fetchCountryExposure(r.Context(), product.ID, asOfDate)
	mcapExp, _ := h.fetchMarketCapExposure(r.Context(), product.ID, asOfDate)

	respondOK(w, map[string]interface{}{
		"as_of_date":          asOfDate,
		"sector_exposure":     sectorExp,
		"country_exposure":    countryExp,
		"market_cap_exposure": mcapExp,
	})
}

// ─── Private helpers ─────────────────────────────────────────────────────────

func (h *FactsheetHandler) fetchProduct(ctx context.Context, slug string) (models.Product, error) {
	var p models.Product
	err := h.db.QueryRow(ctx, `
		SELECT id, slug, name, description, benchmark, currency, inception_date
		FROM products WHERE slug=$1 AND is_active=true
	`, slug).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Benchmark, &p.Currency, &p.InceptionDate)
	return p, err
}

func (h *FactsheetHandler) fetchLatestNAV(ctx context.Context, productID int) (string, float64, error) {
	var date time.Time
	var nav float64
	err := h.db.QueryRow(ctx, `
		SELECT date, nav FROM nav_history
		WHERE product_id=$1 ORDER BY date DESC LIMIT 1
	`, productID).Scan(&date, &nav)
	if err != nil {
		return "", 0, err
	}
	return date.Format("2006-01-02"), nav, nil
}

func (h *FactsheetHandler) fetchPerformance(ctx context.Context, productID int, asOfDate string) (models.PerformanceMetrics, error) {
	var pm models.PerformanceMetrics
	err := h.db.QueryRow(ctx, `
		SELECT as_of_date,
		       return_1m, return_3m, return_6m, return_ytd,
		       return_1y, return_3y, return_inception,
		       volatility_1y, sharpe_ratio, max_drawdown, benchmark_return_1y
		FROM performance_metrics
		WHERE product_id=$1 AND as_of_date=(
		    SELECT MAX(as_of_date) FROM performance_metrics WHERE product_id=$1
		)
	`, productID).Scan(
		&pm.AsOfDate,
		&pm.Return1M, &pm.Return3M, &pm.Return6M, &pm.ReturnYTD,
		&pm.Return1Y, &pm.Return3Y, &pm.ReturnInception,
		&pm.Volatility1Y, &pm.SharpeRatio, &pm.MaxDrawdown, &pm.BenchmarkReturn1Y,
	)
	return pm, err
}

func (h *FactsheetHandler) fetchHoldings(ctx context.Context, productID int, asOfDate string, limit int) ([]models.Holding, error) {
	rows, err := h.db.Query(ctx, `
		SELECT rank, ticker, name, sector, country, weight, COALESCE(market_cap, 0)
		FROM holdings
		WHERE product_id=$1 AND as_of_date=(
		    SELECT MAX(as_of_date) FROM holdings WHERE product_id=$1
		)
		ORDER BY rank LIMIT $2
	`, productID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var holdings []models.Holding
	for rows.Next() {
		var h models.Holding
		if err := rows.Scan(&h.Rank, &h.Ticker, &h.Name, &h.Sector,
			&h.Country, &h.Weight, &h.MarketCap); err != nil {
			continue
		}
		holdings = append(holdings, h)
	}
	return holdings, nil
}

func (h *FactsheetHandler) fetchSectorExposure(ctx context.Context, productID int, asOfDate string) ([]models.SectorWeight, error) {
	rows, err := h.db.Query(ctx, `
		SELECT sector, weight FROM sector_exposure
		WHERE product_id=$1 AND as_of_date=(
		    SELECT MAX(as_of_date) FROM sector_exposure WHERE product_id=$1
		)
		ORDER BY weight DESC
	`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.SectorWeight
	for rows.Next() {
		var sw models.SectorWeight
		if err := rows.Scan(&sw.Sector, &sw.Weight); err != nil {
			continue
		}
		out = append(out, sw)
	}
	return out, nil
}

func (h *FactsheetHandler) fetchCountryExposure(ctx context.Context, productID int, asOfDate string) ([]models.CountryWeight, error) {
	rows, err := h.db.Query(ctx, `
		SELECT country, region, weight FROM country_exposure
		WHERE product_id=$1 AND as_of_date=(
		    SELECT MAX(as_of_date) FROM country_exposure WHERE product_id=$1
		)
		ORDER BY weight DESC
	`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.CountryWeight
	for rows.Next() {
		var cw models.CountryWeight
		if err := rows.Scan(&cw.Country, &cw.Region, &cw.Weight); err != nil {
			continue
		}
		out = append(out, cw)
	}
	return out, nil
}

func (h *FactsheetHandler) fetchMarketCapExposure(ctx context.Context, productID int, asOfDate string) ([]models.MarketCapWeight, error) {
	rows, err := h.db.Query(ctx, `
		SELECT cap_bucket, weight FROM market_cap_exposure
		WHERE product_id=$1 AND as_of_date=(
		    SELECT MAX(as_of_date) FROM market_cap_exposure WHERE product_id=$1
		)
		ORDER BY weight DESC
	`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.MarketCapWeight
	for rows.Next() {
		var mw models.MarketCapWeight
		if err := rows.Scan(&mw.CapBucket, &mw.Weight); err != nil {
			continue
		}
		out = append(out, mw)
	}
	return out, nil
}

func (h *FactsheetHandler) fetchConstituents(ctx context.Context, productID int) ([]models.ConstituentETF, error) {
	rows, err := h.db.Query(ctx, `
		SELECT ticker, name, weight, asset_class, region
		FROM constituent_etfs WHERE product_id=$1 ORDER BY weight DESC
	`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.ConstituentETF
	for rows.Next() {
		var e models.ConstituentETF
		if err := rows.Scan(&e.Ticker, &e.Name, &e.Weight, &e.AssetClass, &e.Region); err != nil {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (h *FactsheetHandler) fetchNAVHistory(ctx context.Context, productID, days int) ([]models.NAVPoint, error) {
	rows, err := h.db.Query(ctx, `
		SELECT date, nav, COALESCE(day_return, 0)
		FROM nav_history
		WHERE product_id=$1 AND date >= NOW() - ($2 || ' days')::INTERVAL
		ORDER BY date ASC
	`, productID, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.NAVPoint
	for rows.Next() {
		var pt models.NAVPoint
		var date time.Time
		if err := rows.Scan(&date, &pt.NAV, &pt.DayReturn); err != nil {
			continue
		}
		pt.Date = date.Format("2006-01-02")
		out = append(out, pt)
	}
	return out, nil
}

// ─── Response helpers ─────────────────────────────────────────────────────────

func respondOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(models.APIResponse{Success: true, Data: data})
}

func respondError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.APIResponse{Success: false, Error: msg})
}
