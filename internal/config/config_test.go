package config

import (
	"testing"
	"time"
)

func TestLoadSentryConfig(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://public@example.com/1")
	t.Setenv("SENTRY_TRACES_SAMPLE_RATE", "0.25")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SentryDSN != "https://public@example.com/1" {
		t.Fatalf("unexpected DSN: %q", cfg.SentryDSN)
	}
	if cfg.SentryTracesSampleRate != 0.25 {
		t.Fatalf("unexpected trace sample rate: %f", cfg.SentryTracesSampleRate)
	}
}

func TestLoadDefaultsToSecureCookiesInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("COOKIE_SECURE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure {
		t.Fatal("expected secure cookies in production")
	}
}

func TestLoadRejectsPartialGoogleConfig(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	t.Setenv("GOOGLE_REDIRECT_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected partial Google configuration error")
	}
}

func TestLoadPoolDefaults(t *testing.T) {
	t.Setenv("DB_MIN_CONNS", "")
	t.Setenv("DB_MAX_CONNS", "")
	t.Setenv("LEADERBOARD_CACHE_TTL", "")
	t.Setenv("SEASON_AWARDS_CACHE_TTL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBMinConns != 2 {
		t.Fatalf("expected 2 warm connections by default, got %d", cfg.DBMinConns)
	}
	if cfg.DBMaxConns != 0 {
		t.Fatalf("expected 0 to keep the pgxpool max default, got %d", cfg.DBMaxConns)
	}
	if cfg.LeaderboardCacheTTL != 15*time.Second {
		t.Fatalf("expected a 15s leaderboard cache, got %s", cfg.LeaderboardCacheTTL)
	}
	if cfg.SeasonAwardsCacheTTL != time.Minute {
		t.Fatalf("expected a 60s season awards cache, got %s", cfg.SeasonAwardsCacheTTL)
	}
}

func TestLoadRejectsMaxConnsBelowMinConns(t *testing.T) {
	t.Setenv("DB_MIN_CONNS", "4")
	t.Setenv("DB_MAX_CONNS", "2")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error when DB_MAX_CONNS is below DB_MIN_CONNS")
	}
}

func TestLoadRejectsNegativeCacheTTL(t *testing.T) {
	t.Setenv("LEADERBOARD_CACHE_TTL", "-5s")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a negative leaderboard cache TTL")
	}
}

func TestLoadRejectsNegativeSeasonAwardsCacheTTL(t *testing.T) {
	t.Setenv("SEASON_AWARDS_CACHE_TTL", "-5s")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a negative season awards cache TTL")
	}
}

func TestLoadAllowsDisabledSeasonAwardsCache(t *testing.T) {
	t.Setenv("SEASON_AWARDS_CACHE_TTL", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SeasonAwardsCacheTTL != 0 {
		t.Fatalf("expected a zero TTL to disable caching, got %s", cfg.SeasonAwardsCacheTTL)
	}
}

func TestLoadAllowsDisabledLeaderboardCache(t *testing.T) {
	t.Setenv("LEADERBOARD_CACHE_TTL", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LeaderboardCacheTTL != 0 {
		t.Fatalf("expected a zero TTL to disable caching, got %s", cfg.LeaderboardCacheTTL)
	}
}

func TestLoadRejectsInvalidSentryTracesSampleRate(t *testing.T) {
	t.Setenv("SENTRY_TRACES_SAMPLE_RATE", "1.1")

	if _, err := Load(); err == nil {
		t.Fatal("expected an invalid trace sample rate error")
	}
}
