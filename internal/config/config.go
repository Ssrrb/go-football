// Package config loads and validates runtime configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config contains settings for the web process.
type Config struct {
	Environment            string
	HTTPAddress            string
	DatabaseURL            string
	RedisAddress           string
	RedisPassword          string
	RedisDB                int
	EditionID              string
	ScoringModelID         string
	ShutdownTimeout        time.Duration
	SentryDSN              string
	SentryTracesSampleRate float64
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	redisDB, err := strconv.Atoi(value("REDIS_DB", "0"))
	if err != nil || redisDB < 0 {
		return Config{}, fmt.Errorf("REDIS_DB must be a non-negative integer")
	}

	shutdownTimeout, err := time.ParseDuration(value("SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || shutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}

	tracesSampleRate, err := strconv.ParseFloat(value("SENTRY_TRACES_SAMPLE_RATE", "0"), 64)
	if err != nil || tracesSampleRate < 0 || tracesSampleRate > 1 {
		return Config{}, fmt.Errorf("SENTRY_TRACES_SAMPLE_RATE must be between 0 and 1")
	}

	cfg := Config{
		Environment:            value("APP_ENV", "development"),
		HTTPAddress:            value("HTTP_ADDR", ":8080"),
		DatabaseURL:            value("DATABASE_URL", "postgres://football:football@localhost:5432/football?sslmode=disable"),
		RedisAddress:           value("REDIS_ADDR", "localhost:6379"),
		RedisPassword:          os.Getenv("REDIS_PASSWORD"),
		RedisDB:                redisDB,
		EditionID:              os.Getenv("EDITION_ID"),
		ScoringModelID:         os.Getenv("SCORING_MODEL_ID"),
		ShutdownTimeout:        shutdownTimeout,
		SentryDSN:              os.Getenv("SENTRY_DSN"),
		SentryTracesSampleRate: tracesSampleRate,
	}
	return cfg, nil
}

func value(name, fallback string) string {
	if current := os.Getenv(name); current != "" {
		return current
	}
	return fallback
}
