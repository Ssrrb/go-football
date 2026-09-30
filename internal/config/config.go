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
	DBMinConns             int
	DBMaxConns             int
	LeaderboardCacheTTL    time.Duration
	SeasonAwardsCacheTTL   time.Duration
	RedisAddress           string
	RedisPassword          string
	RedisDB                int
	EditionID              string
	ScoringModelID         string
	ShutdownTimeout        time.Duration
	SentryDSN              string
	SentryTracesSampleRate float64
	SessionDuration        time.Duration
	CookieSecure           bool
	GoogleClientID         string
	GoogleClientSecret     string
	GoogleRedirectURL      string
}

// Load reads configuration from environment variables.
func Load() (Config, error) {
	redisDB, err := strconv.Atoi(value("REDIS_DB", "0"))
	if err != nil || redisDB < 0 {
		return Config{}, fmt.Errorf("REDIS_DB must be a non-negative integer")
	}

	dbMinConns, err := strconv.Atoi(value("DB_MIN_CONNS", "2"))
	if err != nil || dbMinConns < 0 {
		return Config{}, fmt.Errorf("DB_MIN_CONNS must be a non-negative integer")
	}

	// Zero keeps the pgxpool default of the greater of 4 or the CPU count.
	dbMaxConns, err := strconv.Atoi(value("DB_MAX_CONNS", "0"))
	if err != nil || dbMaxConns < 0 {
		return Config{}, fmt.Errorf("DB_MAX_CONNS must be a non-negative integer")
	}
	if dbMaxConns != 0 && dbMaxConns < dbMinConns {
		return Config{}, fmt.Errorf("DB_MAX_CONNS must be at least DB_MIN_CONNS")
	}

	leaderboardCacheTTL, err := time.ParseDuration(value("LEADERBOARD_CACHE_TTL", "15s"))
	if err != nil || leaderboardCacheTTL < 0 {
		return Config{}, fmt.Errorf("LEADERBOARD_CACHE_TTL must be a non-negative duration")
	}

	// Live season-award candidates are a full-season recomputation, so they are
	// cached for longer than the published leaderboard.
	seasonAwardsCacheTTL, err := time.ParseDuration(value("SEASON_AWARDS_CACHE_TTL", "60s"))
	if err != nil || seasonAwardsCacheTTL < 0 {
		return Config{}, fmt.Errorf("SEASON_AWARDS_CACHE_TTL must be a non-negative duration")
	}

	shutdownTimeout, err := time.ParseDuration(value("SHUTDOWN_TIMEOUT", "10s"))
	if err != nil || shutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT must be a positive duration")
	}

	tracesSampleRate, err := strconv.ParseFloat(value("SENTRY_TRACES_SAMPLE_RATE", "0"), 64)
	if err != nil || tracesSampleRate < 0 || tracesSampleRate > 1 {
		return Config{}, fmt.Errorf("SENTRY_TRACES_SAMPLE_RATE must be between 0 and 1")
	}

	sessionDuration, err := time.ParseDuration(value("SESSION_DURATION", "720h"))
	if err != nil || sessionDuration <= 0 {
		return Config{}, fmt.Errorf("SESSION_DURATION must be a positive duration")
	}

	environment := value("APP_ENV", "development")
	cookieSecure, err := strconv.ParseBool(value("COOKIE_SECURE", strconv.FormatBool(environment == "production")))
	if err != nil {
		return Config{}, fmt.Errorf("COOKIE_SECURE must be true or false")
	}

	googleClientID := os.Getenv("GOOGLE_CLIENT_ID")
	googleClientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	googleRedirectURL := os.Getenv("GOOGLE_REDIRECT_URL")
	googleValues := 0
	for _, current := range []string{googleClientID, googleClientSecret, googleRedirectURL} {
		if current != "" {
			googleValues++
		}
	}
	if googleValues != 0 && googleValues != 3 {
		return Config{}, fmt.Errorf("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_REDIRECT_URL must be set together")
	}

	cfg := Config{
		Environment:            environment,
		HTTPAddress:            value("HTTP_ADDR", ":8080"),
		DatabaseURL:            value("DATABASE_URL", "postgres://football:football@localhost:5432/football?sslmode=disable"),
		DBMinConns:             dbMinConns,
		DBMaxConns:             dbMaxConns,
		LeaderboardCacheTTL:    leaderboardCacheTTL,
		SeasonAwardsCacheTTL:   seasonAwardsCacheTTL,
		RedisAddress:           value("REDIS_ADDR", "localhost:6379"),
		RedisPassword:          os.Getenv("REDIS_PASSWORD"),
		RedisDB:                redisDB,
		EditionID:              os.Getenv("EDITION_ID"),
		ScoringModelID:         os.Getenv("SCORING_MODEL_ID"),
		ShutdownTimeout:        shutdownTimeout,
		SentryDSN:              os.Getenv("SENTRY_DSN"),
		SentryTracesSampleRate: tracesSampleRate,
		SessionDuration:        sessionDuration,
		CookieSecure:           cookieSecure,
		GoogleClientID:         googleClientID,
		GoogleClientSecret:     googleClientSecret,
		GoogleRedirectURL:      googleRedirectURL,
	}
	return cfg, nil
}

func value(name, fallback string) string {
	if current := os.Getenv(name); current != "" {
		return current
	}
	return fallback
}
