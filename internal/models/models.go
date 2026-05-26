package models

import "time"

// ─── Product ────────────────────────────────────────────────────────────────

type Product struct {
	ID            int       `json:"id"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Benchmark     string    `json:"benchmark"`
	Currency      string    `json:"currency"`
	InceptionDate time.Time `json:"inception_date"`
}

// ─── NAV ────────────────────────────────────────────────────────────────────

type NAVPoint struct {
	Date      string  `json:"date"`       // "2024-01-15"
	NAV       float64 `json:"nav"`
	DayReturn float64 `json:"day_return"` // %
}

// ─── Performance ────────────────────────────────────────────────────────────

type PerformanceMetrics struct {
	AsOfDate          string   `json:"as_of_date"`
	Return1M          *float64 `json:"return_1m"`
	Return3M          *float64 `json:"return_3m"`
	Return6M          *float64 `json:"return_6m"`
	ReturnYTD         *float64 `json:"return_ytd"`
	Return1Y          *float64 `json:"return_1y"`
	Return3Y          *float64 `json:"return_3y"`
	ReturnInception   *float64 `json:"return_inception"`
	Volatility1Y      *float64 `json:"volatility_1y"`
	SharpeRatio       *float64 `json:"sharpe_ratio"`
	MaxDrawdown       *float64 `json:"max_drawdown"`
	BenchmarkReturn1Y *float64 `json:"benchmark_return_1y"`
}

// ─── Holdings ───────────────────────────────────────────────────────────────

type Holding struct {
	Rank      int     `json:"rank"`
	Ticker    string  `json:"ticker"`
	Name      string  `json:"name"`
	Sector    string  `json:"sector"`
	Country   string  `json:"country"`
	Weight    float64 `json:"weight"`     // %
	MarketCap int64   `json:"market_cap"` // USD
}

// ─── Exposures ───────────────────────────────────────────────────────────────

type SectorWeight struct {
	Sector string  `json:"sector"`
	Weight float64 `json:"weight"` // %
}

type CountryWeight struct {
	Country string  `json:"country"`
	Region  string  `json:"region"`
	Weight  float64 `json:"weight"`
}

type MarketCapWeight struct {
	CapBucket string  `json:"cap_bucket"`
	Weight    float64 `json:"weight"`
}

// ─── Constituent ETFs ────────────────────────────────────────────────────────

type ConstituentETF struct {
	Ticker     string  `json:"ticker"`
	Name       string  `json:"name"`
	Weight     float64 `json:"weight"`
	AssetClass string  `json:"asset_class"`
	Region     string  `json:"region"`
}

// ─── Factsheet (full aggregated response) ────────────────────────────────────

type Factsheet struct {
	Product         Product            `json:"product"`
	AsOfDate        string             `json:"as_of_date"`
	CurrentNAV      float64            `json:"current_nav"`
	Performance     PerformanceMetrics `json:"performance"`
	TopHoldings     []Holding          `json:"top_holdings"`
	SectorExposure  []SectorWeight     `json:"sector_exposure"`
	CountryExposure []CountryWeight    `json:"country_exposure"`
	MarketCapExposure []MarketCapWeight `json:"market_cap_exposure"`
	Constituents    []ConstituentETF   `json:"constituents"`
	NAVHistory      []NAVPoint         `json:"nav_history"` // last 1 year
}

// ─── ETL Log ─────────────────────────────────────────────────────────────────

type ETLRunLog struct {
	ID              int64      `json:"id"`
	ProductID       *int       `json:"product_id"`
	RunType         string     `json:"run_type"`
	Status          string     `json:"status"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	RecordsUpserted *int       `json:"records_upserted"`
	ErrorMessage    *string    `json:"error_message"`
	TriggeredBy     string     `json:"triggered_by"`
}

// ─── API wrapper ─────────────────────────────────────────────────────────────

type APIResponse struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}
