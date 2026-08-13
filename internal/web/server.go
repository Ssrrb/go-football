// Package web exposes the HTML and health-check HTTP interface.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Ssrrb/go-football/internal/auth"
	"github.com/Ssrrb/go-football/internal/leaderboard"
)

//go:embed templates/*.html assets/*
var files embed.FS

// Leaderboard lists current leaderboard entries.
type Leaderboard interface {
	List(context.Context, leaderboard.Query) ([]leaderboard.Entry, error)
}

// Authenticator is the authentication application service used by HTTP handlers.
type Authenticator interface {
	Register(context.Context, string, string, string) (auth.User, error)
	Authenticate(context.Context, string, string) (auth.User, error)
	AuthenticateGoogle(context.Context, string, string) (auth.User, error)
	GoogleEnabled() bool
	GoogleAuthURL(string, string) string
	NewSession(context.Context, string) (string, error)
	UserBySession(context.Context, string) (auth.User, error)
	RevokeSession(context.Context, string) error
	SessionDuration() time.Duration
}

// Check verifies one runtime dependency.
type Check func(context.Context) error

// ErrorReporter sends an operational error to an external monitor.
type ErrorReporter func(context.Context, error)

// Config contains dependencies and active leaderboard identifiers.
type Config struct {
	Logger         *slog.Logger
	Leaderboard    Leaderboard
	EditionID      string
	ScoringModelID string
	DatabaseCheck  Check
	RedisCheck     Check
	ReportError    ErrorReporter
	Auth           Authenticator
	CookieSecure   bool
}

type server struct {
	logger         *slog.Logger
	leaderboard    Leaderboard
	editionID      string
	scoringModelID string
	databaseCheck  Check
	redisCheck     Check
	reportError    ErrorReporter
	auth           Authenticator
	cookieSecure   bool
	templates      *template.Template
}

type pageData struct {
	Entries      []leaderboard.Entry
	Position     string
	Configured   bool
	RefreshURL   string
	ErrorMessage string
	UpdatedAt    time.Time
	User         *auth.User
	CSRFToken    string
}

// New constructs the application HTTP handler.
func New(cfg Config) (http.Handler, error) {
	if cfg.Logger == nil || cfg.Leaderboard == nil || cfg.Auth == nil {
		return nil, fmt.Errorf("logger, leaderboard, and auth are required")
	}
	templates, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		return nil, fmt.Errorf("load assets: %w", err)
	}

	s := &server{
		logger: cfg.Logger, leaderboard: cfg.Leaderboard,
		editionID: cfg.EditionID, scoringModelID: cfg.ScoringModelID,
		databaseCheck: cfg.DatabaseCheck, redisCheck: cfg.RedisCheck,
		reportError: cfg.ReportError, auth: cfg.Auth,
		cookieSecure: cfg.CookieSecure, templates: templates,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("GET /register", s.registerPage)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /auth/google", s.googleStart)
	mux.HandleFunc("GET /auth/google/callback", s.googleCallback)
	mux.HandleFunc("GET /health/live", live)
	mux.HandleFunc("GET /health/ready", s.ready)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	return requestLogger(cfg.Logger, securityHeaders(mux)), nil
}

func (s *server) home(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(response, request)
		return
	}
	position := strings.ToUpper(request.URL.Query().Get("position"))
	if position != "" && position != "GK" && position != "DEF" && position != "MID" && position != "FWD" {
		http.Error(response, "invalid position", http.StatusBadRequest)
		return
	}

	data := s.load(request, position)
	data.User = s.currentUser(response, request)
	data.CSRFToken = s.csrfToken(response, request)
	response.Header().Add("Vary", "HX-Request, HX-History-Restore-Request")
	if request.Header.Get("HX-Request") == "true" && request.Header.Get("HX-History-Restore-Request") != "true" {
		s.render(response, request, http.StatusOK, "leaderboard", data)
		return
	}
	s.render(response, request, http.StatusOK, "base", data)
}

func (s *server) load(request *http.Request, position string) pageData {
	refreshURL := "/"
	if position != "" {
		refreshURL += "?position=" + url.QueryEscape(position)
	}
	data := pageData{
		Position:   position,
		Configured: s.editionID != "" && s.scoringModelID != "",
		RefreshURL: refreshURL,
		UpdatedAt:  time.Now().UTC(),
	}
	if !data.Configured {
		return data
	}
	entries, err := s.leaderboard.List(request.Context(), leaderboard.Query{
		EditionID: s.editionID, ScoringModelID: s.scoringModelID,
		Position: position, Limit: 50,
	})
	if err != nil {
		s.report(request.Context(), "load leaderboard", err)
		data.ErrorMessage = "The leaderboard is temporarily unavailable."
		return data
	}
	data.Entries = entries
	return data
}

func (s *server) render(response http.ResponseWriter, request *http.Request, status int, name string, data any) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(response, name, data); err != nil {
		s.report(request.Context(), "render template "+name, err)
	}
}

func (s *server) report(ctx context.Context, operation string, err error) {
	err = fmt.Errorf("%s: %w", operation, err)
	s.logger.Error(operation, "error", err)
	if s.reportError != nil {
		s.reportError(ctx, err)
	}
}

func live(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(`{"status":"ok"}`))
}

func (s *server) ready(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	response.Header().Set("Content-Type", "application/json")
	checks := []struct {
		name  string
		check Check
	}{
		{name: "database", check: s.databaseCheck},
		{name: "redis", check: s.redisCheck},
	}
	for _, dependency := range checks {
		if dependency.check == nil || dependency.check(ctx) != nil {
			s.logger.Warn("readiness check failed", "dependency", dependency.name)
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = response.Write([]byte(`{"status":"unavailable"}`))
			return
		}
	}
	_, _ = response.Write([]byte(`{"status":"ok"}`))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'")
		response.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(response, request)
		logger.Info("http request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
	})
}
