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

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisClient := redis.NewClient(&redis.Options{
		Addr: cfg.RedisAddress, Password: cfg.RedisPassword, DB: cfg.RedisDB,
	})
	defer redisClient.Close()

	repository := leaderboard.NewPostgresRepository(pool)
	leaderboards := leaderboard.NewService(repository, leaderboard.NewRedisCache(redisClient))
	handler, err := webapp.New(webapp.Config{
		Logger:         logger,
		Leaderboard:    leaderboards,
		EditionID:      cfg.EditionID,
		ScoringModelID: cfg.ScoringModelID,
		DatabaseCheck:  pool.Ping,
		RedisCheck: func(ctx context.Context) error {
			return redisClient.Ping(ctx).Err()
		},
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
