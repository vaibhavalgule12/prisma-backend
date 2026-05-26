package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/springstreet/prisma-backend/internal/api"
	"github.com/springstreet/prisma-backend/internal/config"
	"github.com/springstreet/prisma-backend/internal/db"
)

func main() {
	// ── Logger ───────────────────────────────────────────────────────────────
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(log)

	// ── Config ───────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	// ── Database ─────────────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := db.New(ctx, cfg.DSN())
	if err != nil {
		log.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("run migrations", "err", err)
		os.Exit(1)
	}
	log.Info("database ready")

	// ── HTTP server ──────────────────────────────────────────────────────────
	router := api.NewRouter(pool, log)
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info("server starting", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-quit
	log.Info("shutting down…")

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("shutdown error", "err", err)
	}
	log.Info("server stopped")
}
