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

//go:embed templates/*.html assets/* public/clubs/*
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

type seasonAward struct {
	Model       string
	Category    string
	PlayerName  string
	ClubName    string
	ClubIconURL string
}

type pageData struct {
	Entries           []leaderboard.Entry
	TopEntries        []leaderboard.Entry
	SeasonAwards      []seasonAward
	SeasonAwardModels []seasonAward
	Position          string
	Model             string
	Configured        bool
	RatingMode        bool
	PageURL           string
	RefreshURL        string
	ActivePage        string
	ErrorMessage      string
	UpdatedAt         time.Time
	User              *auth.User
	CSRFToken         string
}

// New constructs the application HTTP handler.
func New(cfg Config) (http.Handler, error) {
	if cfg.Logger == nil || cfg.Leaderboard == nil || cfg.Auth == nil {
		return nil, fmt.Errorf("logger, leaderboard, and auth are required")
	}
	templates, err := template.New("").Funcs(template.FuncMap{
		"clubLogoURL": clubLogoURL,
	}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		return nil, fmt.Errorf("load assets: %w", err)
	}
	public, err := fs.Sub(files, "public")
	if err != nil {
		return nil, fmt.Errorf("load public assets: %w", err)
	}

	s := &server{
		logger: cfg.Logger, leaderboard: cfg.Leaderboard,
		editionID: cfg.EditionID, scoringModelID: cfg.ScoringModelID,
		databaseCheck: cfg.DatabaseCheck, redisCheck: cfg.RedisCheck,
		reportError: cfg.ReportError, auth: cfg.Auth,
		cookieSecure: cfg.CookieSecure, templates: templates,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", s.requireAuth(http.HandlerFunc(s.home)))
	mux.Handle("GET /clasificacion", s.requireAuth(http.HandlerFunc(s.classification)))
	mux.Handle("GET /metodologia", s.requireAuth(http.HandlerFunc(s.methodology)))
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
	mux.Handle("GET /public/", http.StripPrefix("/public/", http.FileServer(http.FS(public))))
	return requestLogger(cfg.Logger, securityHeaders(mux)), nil
}

func (s *server) home(response http.ResponseWriter, request *http.Request) {
	s.leaderboardPage(response, request, "/", "overview")
}

func (s *server) classification(response http.ResponseWriter, request *http.Request) {
	s.leaderboardPage(response, request, "/clasificacion", "classification")
}

func (s *server) leaderboardPage(response http.ResponseWriter, request *http.Request, pageURL, activePage string) {
	position := strings.ToUpper(request.URL.Query().Get("position"))
	if position != "" && position != "GK" && position != "DEF" && position != "MID" && position != "FWD" {
		http.Error(response, "invalid position", http.StatusBadRequest)
		return
	}
	model := request.URL.Query().Get("model")
	if !validSeasonAwardModel(model) {
		http.Error(response, "invalid model", http.StatusBadRequest)
		return
	}

	data := s.load(request, position, model, pageURL)
	data.ActivePage = activePage
	data.User = s.currentUser(response, request)
	data.CSRFToken = s.csrfToken(response, request)
	response.Header().Add("Vary", "HX-Request, HX-History-Restore-Request")
	if request.Header.Get("HX-Request") == "true" && request.Header.Get("HX-History-Restore-Request") != "true" {
		s.render(response, request, http.StatusOK, "leaderboard", data)
		return
	}
	s.render(response, request, http.StatusOK, "base", data)
}

func (s *server) methodology(response http.ResponseWriter, request *http.Request) {
	data := pageData{
		ActivePage: "methodology",
		User:       s.currentUser(response, request),
		CSRFToken:  s.csrfToken(response, request),
	}
	s.render(response, request, http.StatusOK, "methodology", data)
}

func (s *server) load(request *http.Request, position, model, pageURL string) pageData {
	query := url.Values{}
	if position != "" {
		query.Set("position", position)
	}
	if model != "" {
		query.Set("model", model)
	}
	refreshURL := pageURL
	if encoded := query.Encode(); encoded != "" {
		refreshURL += "?" + encoded
	}

	models := seasonAwardModels()
	data := pageData{
		SeasonAwards:      filterSeasonAwards(models, model),
		SeasonAwardModels: models,
		Position:          position,
		Model:             model,
		Configured:        s.editionID != "" && s.scoringModelID != "",
		RatingMode:        s.scoringModelID == leaderboard.PlayerMatchRatingModelID,
		PageURL:           pageURL,
		RefreshURL:        refreshURL,
		UpdatedAt:         time.Now().UTC(),
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
		data.ErrorMessage = "La clasificación no está disponible temporalmente."
		return data
	}
	data.Entries = entries
	topCount := min(3, len(entries))
	data.TopEntries = entries[:topCount]
	return data
}

func seasonAwardModels() []seasonAward {
	// Winner fields stay empty until a published season-award selection is loaded.
	return []seasonAward{
		{Model: "player-of-the-season", Category: "Jugador de la temporada"},
		{Model: "defender-of-the-season", Category: "Defensa de la temporada"},
		{Model: "goalkeeper-of-the-season", Category: "Portero de la temporada"},
		{Model: "young-player-of-the-season", Category: "Jugador joven de la temporada (Sub-23)"},
		{Model: "decisive-substitute", Category: "Suplente más decisivo de la temporada"},
		{Model: "most-improved-player", Category: "Jugador con mayor progresión"},
	}
}

func validSeasonAwardModel(model string) bool {
	if model == "" {
		return true
	}
	for _, award := range seasonAwardModels() {
		if award.Model == model {
			return true
		}
	}
	return false
}

func filterSeasonAwards(awards []seasonAward, model string) []seasonAward {
	if model == "" {
		return awards
	}
	if award := findSeasonAward(awards, model); award != nil {
		return []seasonAward{*award}
	}
	return nil
}

func findSeasonAward(awards []seasonAward, model string) *seasonAward {
	for index := range awards {
		if awards[index].Model == model {
			return &awards[index]
		}
	}
	return nil
}

func clubLogoURL(teamName string) string {
	normalized := strings.ToLower(strings.TrimSpace(teamName))
	normalized = strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n",
	).Replace(normalized)
	normalized = strings.TrimPrefix(normalized, "club ")

	filenames := map[string]string{
		"2 de mayo":              "Escudo_Oficial_del_Club_2_de_Mayo_de_Paraguay.svg.png",
		"cerro porteno":          "cerro-porteno.png",
		"guarani":                "club-guarani.png",
		"libertad":               "250px-Club_Libertad.png",
		"nacional":               "Club_Nacional_de_Paraguay.png",
		"nacional asuncion":      "Club_Nacional_de_Paraguay.png",
		"nacional de paraguay":   "Club_Nacional_de_Paraguay.png",
		"olimpia":                "Escudo_original_de_Olimpia.png",
		"sportivo luqueno":       "EscudoLuque2022.png",
		"sportivo san lorenzo":   "Escudo_Sportivo_San_Lorenzo.png",
		"sportivo trinidense":    "Club_Sportivo_Trinidense.svg.png",
		"recoleta":               "Recoleta_Football_Club_logo_Paraguay_official_crest.png",
		"recoleta fc":            "Recoleta_Football_Club_logo_Paraguay_official_crest.png",
		"recoleta football club": "Recoleta_Football_Club_logo_Paraguay_official_crest.png",
		"sportivo ameliano":      "Sportivo_Ameliano_1936.png",
	}
	if filename := filenames[normalized]; filename != "" {
		return "/public/clubs/" + filename
	}
	return ""
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
