package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all application configuration.
type Config struct {
	// Server
	Port string

	// Database
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// ETL
	ETLSchedule string // cron expression, e.g. "0 18 * * 1-5"
}

// Load reads .env (if present) then environment variables.
func Load() (*Config, error) {
	// Best-effort load of .env — fine to skip in prod containers.
	_ = godotenv.Load()

	cfg := &Config{
		Port:        getEnv("PORT", "8080"),
		DBHost:      getEnv("DB_HOST", "localhost"),
		DBUser:      getEnv("DB_USER", "prisma"),
		DBPassword:  getEnv("DB_PASSWORD", "prisma"),
		DBName:      getEnv("DB_NAME", "prismadb"),
		DBSSLMode:   getEnv("DB_SSLMODE", "disable"),
		ETLSchedule: getEnv("ETL_SCHEDULE", "0 18 * * 1-5"), // 6 PM weekdays (after US market close)
	}

	port, err := strconv.Atoi(getEnv("DB_PORT", "5432"))
	if err != nil {
		return nil, fmt.Errorf("invalid DB_PORT: %w", err)
	}
	cfg.DBPort = port

	return cfg, nil
}

// DSN returns a pgx-compatible connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
