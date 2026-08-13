package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ssrrb/go-football/internal/leaderboard"
)

type leaderboardStub struct{}

func (leaderboardStub) List(context.Context, leaderboard.Query) ([]leaderboard.Entry, error) {
	return []leaderboard.Entry{{CompetitionRank: 1, PlayerName: "Ada Striker", PositionCode: "FWD", PointsMilli: 1250}}, nil
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	check := func(context.Context) error { return nil }
	handler, err := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Leaderboard: leaderboardStub{},
		EditionID: "edition", ScoringModelID: "model", DatabaseCheck: check, RedisCheck: check,
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
