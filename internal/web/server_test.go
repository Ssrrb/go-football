package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ssrrb/go-football/internal/auth"
	"github.com/Ssrrb/go-football/internal/awards"
	"github.com/Ssrrb/go-football/internal/leaderboard"
)

type leaderboardStub struct{}

type seasonAwardsStub struct{}

func (seasonAwardsStub) TopCandidates(_ context.Context, _ string, codes []string, _ int) (map[string][]awards.Candidate, error) {
	result := make(map[string][]awards.Candidate, len(codes))
	for _, code := range codes {
		result[code] = []awards.Candidate{
			{AwardCode: code, Rank: 1, PlayerName: "Ada Striker", TeamName: "Club Cerro Porteño", ScoreMilli: 95420, Projection: true},
			{AwardCode: code, Rank: 2, PlayerName: "Bea Keeper", TeamName: "Club Guaraní", ScoreMilli: 90100, Projection: true},
			{AwardCode: code, Rank: 3, PlayerName: "Cris Defender", TeamName: "Olimpia", ScoreMilli: 87500, Projection: true},
		}
	}
	return result, nil
}

type authStub struct{}

func (authStub) Register(context.Context, string, string, string) (auth.User, error) {
	return auth.User{ID: "user", Email: "ada@example.com", DisplayName: "Ada"}, nil
}
func (authStub) Authenticate(context.Context, string, string) (auth.User, error) {
	return auth.User{ID: "user", Email: "ada@example.com", DisplayName: "Ada"}, nil
}
func (authStub) AuthenticateGoogle(context.Context, string, string) (auth.User, error) {
	return auth.User{}, auth.ErrInvalidGoogleUser
}
func (authStub) GoogleEnabled() bool                                { return false }
func (authStub) GoogleAuthURL(string, string) string                { return "" }
func (authStub) NewSession(context.Context, string) (string, error) { return "session", nil }
func (authStub) UserBySession(_ context.Context, token string) (auth.User, error) {
	if token == "session" {
		return auth.User{ID: "user", Email: "ada@example.com", DisplayName: "Ada"}, nil
	}
	return auth.User{}, auth.ErrInvalidSession
}
func (authStub) RevokeSession(context.Context, string) error { return nil }
func (authStub) SessionDuration() time.Duration              { return 24 * time.Hour }

func (leaderboardStub) List(context.Context, leaderboard.Query) ([]leaderboard.Entry, error) {
	return []leaderboard.Entry{{CompetitionRank: 1, PlayerName: "Ada Striker", TeamName: "Club Cerro Porteño", PositionCode: "FWD", Appearances: 10, Minutes: 900, PointsMilli: 51_250}}, nil
}

func testHandler(t *testing.T) http.Handler {
	return testHandlerWithScoringModel(t, "model")
}

func authenticatedRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "session"})
	return request
}

func testHandlerWithScoringModel(t *testing.T, scoringModelID string) http.Handler {
	t.Helper()
	check := func(context.Context) error { return nil }
	handler, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Leaderboard: leaderboardStub{},
		SeasonAwards: seasonAwardsStub{}, EditionID: "edition", ScoringModelID: scoringModelID,
		DatabaseCheck: check, RedisCheck: check,
		Auth: authStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestProtectedPagesRedirectToLoginWithoutSession(t *testing.T) {
	for _, path := range []string{"/", "/clasificacion", "/metodologia"} {
		response := httptest.NewRecorder()
		testHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
			t.Fatalf("%s: expected redirect to login, got %d %q", path, response.Code, response.Header().Get("Location"))
		}
	}
}

func TestProtectedHTMXRequestRedirectsToLoginWithoutSession(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/clasificacion", nil)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("expected HTMX redirect to login, got %d %q", response.Code, response.Header().Get("HX-Redirect"))
	}
}

func TestHomeRendersLeaderboard(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Ada Striker") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestHomeUsesExternalStylesAndShowsAuthenticatedUser(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/", nil))
	body := response.Body.String()
	if !strings.Contains(body, `href="/assets/app.css"`) || strings.Contains(body, "<style") {
		t.Fatal("expected styles to be loaded from the CSS asset")
	}
	if !strings.Contains(body, `class="sidebar-logout"`) || !strings.Contains(body, "Ada") {
		t.Fatal("expected the authenticated user and logout button in the sidebar")
	}
}

func TestHomeShowsSeasonAwardCardsWithoutPlayerPhotos(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/", nil))
	body := response.Body.String()
	categories := []string{
		"Jugador de la temporada",
		"Defensa de la temporada",
		"Portero de la temporada",
		"Jugador joven de la temporada (Sub-23)",
		"Suplente más decisivo de la temporada",
		"Jugador con mayor progresión",
	}
	for _, category := range categories {
		if !strings.Contains(body, category) {
			t.Fatalf("expected season award %q", category)
		}
	}
	if count := strings.Count(body, `class="card award"`); count != len(categories) {
		t.Fatalf("expected %d season award cards, got %d", len(categories), count)
	}
	if strings.Contains(body, "award-player-photo") {
		t.Fatal("season award cards must not render player photos")
	}
}

func TestHomeShowsTopThreePlayersForEverySeasonAward(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/", nil))
	body := response.Body.String()

	if count := strings.Count(body, `class="award-ranking"`); count != 6 {
		t.Fatalf("expected a top-three ranking for all 6 awards, got %d", count)
	}
	if count := strings.Count(body, `class="award-rank">3</span>`); count != 6 {
		t.Fatalf("expected each award to show its third-ranked player, got %d", count)
	}
	if !strings.Contains(body, "Ada Striker") || !strings.Contains(body, "95.420 pts") {
		t.Fatal("expected player names and award scores")
	}
	if !strings.Contains(body, "Proyección en vivo") || !strings.Contains(body, `hx-trigger="every 15s"`) {
		t.Fatal("expected provisional awards to refresh automatically")
	}
}

func TestSeasonAwardsFragmentRecalculatesSelectedCategory(t *testing.T) {
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/premios?model=goalkeeper-of-the-season", nil)
	request.Header.Set("HX-Request", "true")
	testHandler(t).ServeHTTP(response, request)
	body := response.Body.String()

	if response.Code != http.StatusOK || strings.Count(body, `class="card award"`) != 1 {
		t.Fatalf("unexpected awards fragment: %d %s", response.Code, body)
	}
	if strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `hx-get="/premios?model=goalkeeper-of-the-season"`) {
		t.Fatal("expected a self-refreshing awards fragment that preserves the category")
	}
}

func TestHomeFiltersSeasonAwardsByModel(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(
		http.MethodGet,
		"/?position=DEF&model=defender-of-the-season",
		nil,
	))

	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, body)
	}
	if count := strings.Count(body, `class="card award"`); count != 1 {
		t.Fatalf("expected one filtered season award card, got %d", count)
	}
	if !strings.Contains(body, `<option value="defender-of-the-season" selected>Defensa de la temporada</option>`) {
		t.Fatal("expected defender model to remain selected")
	}
	if !strings.Contains(body, `hx-get="/?model=defender-of-the-season&amp;position=DEF"`) {
		t.Fatal("expected automatic leaderboard refresh to preserve both filters")
	}
}

func TestHomeRejectsInvalidSeasonAwardModel(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/?model=unknown", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid model to return 400, got %d", response.Code)
	}
}

func TestHomeShowsTeamColumnAndPlayerClubLogo(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/", nil))
	body := response.Body.String()
	if !strings.Contains(body, `<th scope="col">Equipo</th>`) {
		t.Fatal("expected team column in leaderboard")
	}
	if !strings.Contains(body, `src="/public/clubs/cerro-porteno.png" alt="Escudo de Club Cerro Porteño"`) {
		t.Fatal("expected the player's team logo")
	}
}

func TestClubLogoURLSupportsDatabaseTeamNames(t *testing.T) {
	for teamName, expected := range map[string]string{
		"Club Guaraní":          "/public/clubs/club-guarani.png",
		"Club Sportivo Luqueño": "/public/clubs/EscudoLuque2022.png",
		"Recoleta FC":           "/public/clubs/Recoleta_Football_Club_logo_Paraguay_official_crest.png",
	} {
		if actual := clubLogoURL(teamName); actual != expected {
			t.Errorf("clubLogoURL(%q) = %q, want %q", teamName, actual, expected)
		}
	}
}

func TestClubIconsAreServed(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/public/clubs/cerro-porteno.png", nil))
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "image/") {
		t.Fatalf("unexpected club icon response: %d %q", response.Code, response.Header().Get("Content-Type"))
	}
}

func TestClassificationAndMethodologyPages(t *testing.T) {
	for _, path := range []string{"/clasificacion?position=FWD", "/metodologia"} {
		response := httptest.NewRecorder()
		testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, response.Code)
		}
	}
}

func TestHomeLabelsDefaultModelAsOwnPerformanceRating(t *testing.T) {
	response := httptest.NewRecorder()
	testHandlerWithScoringModel(t, leaderboard.BasePerformanceModelID).ServeHTTP(
		response,
		authenticatedRequest(http.MethodGet, "/", nil),
	)

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `class="points">Rating propio</th>`) {
		t.Fatalf("expected own-performance rating leaderboard: %d %s", response.Code, body)
	}
	if !strings.Contains(body, `class="points">Confianza</th>`) || !strings.Contains(body, `class="points">50%</td>`) {
		t.Fatal("expected sample confidence column")
	}
	if !strings.Contains(body, "The default rating uses normalized match statistics, positional weights, and minutes reliability") {
		t.Fatal("expected provider-independent rating explanation")
	}
}

func TestHomeReturnsLeaderboardFragmentForHTMX(t *testing.T) {
	response := httptest.NewRecorder()
	request := authenticatedRequest(http.MethodGet, "/?position=FWD", nil)
	request.Header.Set("HX-Request", "true")
	testHandler(t).ServeHTTP(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `<section id="leaderboard"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, body)
	}
	if strings.Contains(body, "<!doctype html>") {
		t.Fatal("HTMX request returned the full page")
	}
	if vary := response.Header().Get("Vary"); !strings.Contains(vary, "HX-Request") {
		t.Fatalf("expected HX-Request vary header, got %q", vary)
	}
}

func TestLoginPageProvidesCSRFProtectedForm(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/login", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `action="/login"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) == 0 {
		t.Fatal("expected a CSRF cookie")
	}
}

func TestHTMXLoginStartsSessionAndRedirects(t *testing.T) {
	handler := testHandler(t)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/login", nil))
	csrfCookie := page.Result().Cookies()[0]

	body := "csrf_token=" + csrfCookie.Value + "&email=ada%40example.com&password=password123"
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	request.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || response.Header().Get("HX-Redirect") != "/" {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestHomeRejectsInvalidPosition(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/?position=CB", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

func TestLiveness(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}
}
