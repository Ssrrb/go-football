package config

import "testing"

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

func TestLoadRejectsInvalidSentryTracesSampleRate(t *testing.T) {
	t.Setenv("SENTRY_TRACES_SAMPLE_RATE", "1.1")

	if _, err := Load(); err == nil {
		t.Fatal("expected an invalid trace sample rate error")
	}
}
