package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ssrrb/go-football/internal/auth"
	"github.com/Ssrrb/go-football/internal/awards"
	"github.com/Ssrrb/go-football/internal/config"
	"github.com/Ssrrb/go-football/internal/leaderboard"
	webapp "github.com/Ssrrb/go-football/internal/web"
	"github.com/getsentry/sentry-go"
	"github.com/getsentry/sentry-go/http"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("web process stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) (runErr error) {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if cfg.SentryDSN != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:              cfg.SentryDSN,
			Environment:      cfg.Environment,
			EnableTracing:    cfg.SentryTracesSampleRate > 0,
			TracesSampleRate: cfg.SentryTracesSampleRate,
			Tags:             map[string]string{"service": "web"},
		}); err != nil {
			return fmt.Errorf("initialize Sentry: %w", err)
		}
		defer func() {
			if runErr != nil {
				sentry.CaptureException(runErr)
			}
			sentry.Flush(2 * time.Second)
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	// A TLS handshake against a remote pooler costs far more than the queries
	// themselves, so keep idle connections warm instead of paying that cost on
	// the first request that needs one.
	poolConfig.MinConns = int32(cfg.DBMinConns)
	poolConfig.MinIdleConns = int32(cfg.DBMinConns)
	if cfg.DBMaxConns > 0 {
		poolConfig.MaxConns = int32(cfg.DBMaxConns)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	prewarmPool(ctx, logger, pool, cfg.DBMinConns)

	redisClient := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddress, Password: cfg.RedisPassword, DB: cfg.RedisDB,
	})
	defer redisClient.Close()

	repository := leaderboard.NewPostgresRepository(pool)
	leaderboards := leaderboard.NewCachedService(
		repository, leaderboard.NewRedisCache(redisClient), cfg.LeaderboardCacheTTL,
	)
	seasonAwards := awards.NewCachedService(awards.NewRepository(pool), cfg.SeasonAwardsCacheTTL)
	google := auth.NewGoogleClient(auth.GoogleConfig{
		ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret,
		RedirectURL: cfg.GoogleRedirectURL,
	})
	authService, err := auth.NewService(auth.NewPostgresStore(pool), google, cfg.SessionDuration)
	if err != nil {
		return err
	}
	handler, err := webapp.New(webapp.Config{
		Logger:         logger,
		Leaderboard:    leaderboards,
		SeasonAwards:   seasonAwards,
		EditionID:      cfg.EditionID,
		ScoringModelID: cfg.ScoringModelID,
		DatabaseCheck:  pool.Ping,
		RedisCheck: func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		},
		Auth:         authService,
		CookieSecure: cfg.CookieSecure,
		ReportError: func(ctx context.Context, err error) {
			if hub := sentry.GetHubFromContext(ctx); hub != nil {
				hub.CaptureException(err)
			}
		},
	})
	if err != nil {
		return err
	}

	if cfg.SentryDSN != "" {
		handler = sentryhttp.New(sentryhttp.Options{Repanic: true}).Handle(handler)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("web server listening", "address", cfg.HTTPAddress, "environment", cfg.Environment)
		errorsChannel <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// prewarmPool establishes the minimum number of pooled connections before the
// server accepts traffic, so the first request does not pay for a TLS
// handshake. Failures are logged rather than fatal: the process should still
// start and report its readiness through /health/ready.
func prewarmPool(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, size int) {
	if size <= 0 {
		return
	}
	started := time.Now()
	wait := make(chan struct{}, size)
	for range size {
		go func() {
			connection, err := pool.Acquire(ctx)
			if err == nil {
				connection.Release()
			}
			wait <- struct{}{}
		}()
	}
	for range size {
		<-wait
	}
	logger.Info("database pool warmed", "connections", size, "duration", time.Since(started))
}
