package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/springstreet/prisma-backend/internal/api/handlers"
)

// NewRouter constructs the full HTTP router.
func NewRouter(db *pgxpool.Pool, log *slog.Logger) http.Handler {
	r := chi.NewRouter()

	// ── Global middleware ────────────────────────────────────────────────────
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(middleware.Compress(5))

	// CORS – allow the Spring Street frontend origin
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://springstreet.in", "http://localhost:3000"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// ── Handler init ─────────────────────────────────────────────────────────
	fsHandler := handlers.NewFactsheetHandler(db, log)
	adminHandler := handlers.NewAdminHandler(db, log)

	// ── Routes ───────────────────────────────────────────────────────────────
	r.Route("/api/v1", func(r chi.Router) {

		// Public product endpoints
		r.Get("/products", fsHandler.ListProducts)

		r.Route("/products/{slug}", func(r chi.Router) {
			r.Get("/factsheet", fsHandler.GetFactsheet)   // full factsheet (main endpoint)
			r.Get("/nav", fsHandler.GetNAVHistory)        // NAV time-series
			r.Get("/holdings", fsHandler.GetHoldings)     // top holdings
			r.Get("/exposures", fsHandler.GetExposures)   // sector/country/mcap
		})

		// Admin / ops endpoints (add auth middleware in production)
		r.Route("/admin", func(r chi.Router) {
			r.Post("/sync", adminHandler.TriggerSync)
			r.Get("/sync/logs", adminHandler.GetSyncLogs)
		})

		// Health
		r.Get("/health", adminHandler.Healthcheck)
	})

	return r
}
