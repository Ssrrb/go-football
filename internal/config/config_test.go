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

func TestLoadRejectsInvalidSentryTracesSampleRate(t *testing.T) {
	t.Setenv("SENTRY_TRACES_SAMPLE_RATE", "1.1")

	if _, err := Load(); err == nil {
		t.Fatal("expected an invalid trace sample rate error")
	}
}
