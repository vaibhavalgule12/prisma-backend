package db

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New creates and validates a pgx connection pool.
func New(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}

	// Pool sizing: keeps connections lean for a small service.
	cfg.MaxConns = 20
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	return pool, nil
}

// Migrate runs all SQL migration files in order.
// In production you'd use golang-migrate; this is a lightweight
// alternative suitable for an internship project.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrations := []string{
		"internal/db/migrations/001_init.sql",
	}

	for _, path := range migrations {
		sql, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", path, err)
		}
	}
	return nil
}
