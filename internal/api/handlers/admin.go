package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/springstreet/prisma-backend/internal/models"
)

type AdminHandler struct {
	db  *pgxpool.Pool
	log *slog.Logger
}

func NewAdminHandler(db *pgxpool.Pool, log *slog.Logger) *AdminHandler {
	return &AdminHandler{db: db, log: log}
}

// TriggerSync records a manual sync request and acknowledges it.
// The actual heavy lifting happens in the Python ETL service.
// POST /api/v1/admin/sync
func (h *AdminHandler) TriggerSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProductSlug string `json:"product_slug"` // optional; omit for all products
		RunType     string `json:"run_type"`     // "nav", "holdings", "exposures", "full"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RunType == "" {
		req.RunType = "full"
	}

	// Resolve optional product ID
	var productID *int
	if req.ProductSlug != "" {
		var id int
		err := h.db.QueryRow(r.Context(),
			"SELECT id FROM products WHERE slug=$1", req.ProductSlug,
		).Scan(&id)
		if err != nil {
			respondError(w, http.StatusNotFound, "product not found")
			return
		}
		productID = &id
	}

	// Insert a "running" log entry that the ETL pipeline will pick up and update.
	var logID int64
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO etl_run_log (product_id, run_type, status, triggered_by)
		VALUES ($1, $2, 'running', 'api')
		RETURNING id
	`, productID, req.RunType).Scan(&logID)
	if err != nil {
		h.log.Error("insert etl log", "err", err)
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.log.Info("manual sync requested", "log_id", logID, "run_type", req.RunType)

	respondOK(w, map[string]interface{}{
		"message":    "sync triggered",
		"log_id":     logID,
		"run_type":   req.RunType,
		"started_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// GetSyncLogs returns recent ETL run logs.
// GET /api/v1/admin/sync/logs?limit=20
func (h *AdminHandler) GetSyncLogs(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT l.id, l.product_id, l.run_type, l.status,
		       l.started_at, l.finished_at, l.records_upserted,
		       l.error_message, l.triggered_by
		FROM etl_run_log l
		ORDER BY l.started_at DESC
		LIMIT 50
	`)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()

	var logs []models.ETLRunLog
	for rows.Next() {
		var l models.ETLRunLog
		if err := rows.Scan(
			&l.ID, &l.ProductID, &l.RunType, &l.Status,
			&l.StartedAt, &l.FinishedAt, &l.RecordsUpserted,
			&l.ErrorMessage, &l.TriggeredBy,
		); err != nil {
			continue
		}
		logs = append(logs, l)
	}
	respondOK(w, logs)
}

// Healthcheck returns service health.
// GET /api/v1/health
func (h *AdminHandler) Healthcheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dbOK := h.db.Ping(ctx) == nil

	status := "healthy"
	httpStatus := http.StatusOK
	if !dbOK {
		status = "degraded"
		httpStatus = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    status,
		"db":        dbOK,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
