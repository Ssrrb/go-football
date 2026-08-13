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
	"github.com/Ssrrb/go-football/internal/leaderboard"
)

type leaderboardStub struct{}

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
func (authStub) UserBySession(context.Context, string) (auth.User, error) {
	return auth.User{}, auth.ErrInvalidSession
}
func (authStub) RevokeSession(context.Context, string) error { return nil }
func (authStub) SessionDuration() time.Duration              { return 24 * time.Hour }

func (leaderboardStub) List(context.Context, leaderboard.Query) ([]leaderboard.Entry, error) {
	return []leaderboard.Entry{{CompetitionRank: 1, PlayerName: "Ada Striker", PositionCode: "FWD", PointsMilli: 1250}}, nil
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	check := func(context.Context) error { return nil }
	handler, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Leaderboard: leaderboardStub{},
		EditionID: "edition", ScoringModelID: "model", DatabaseCheck: check, RedisCheck: check,
		Auth: authStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHomeRendersLeaderboard(t *testing.T) {
	response := httptest.NewRecorder()
	testHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Ada Striker") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestHomeReturnsLeaderboardFragmentForHTMX(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?position=FWD", nil)
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
	testHandler(t).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?position=CB", nil))
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
