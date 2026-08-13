package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type storeStub struct {
	created        User
	passwordHash   string
	passwordUser   passwordUser
	passwordErr    error
	sessionHash    string
	sessionExpires time.Time
	googleUser     User
}

func (s *storeStub) CreateUser(_ context.Context, user User, hash string) error {
	s.created, s.passwordHash = user, hash
	return nil
}
func (s *storeStub) PasswordUserByEmail(context.Context, string) (passwordUser, error) {
	return s.passwordUser, s.passwordErr
}
func (s *storeStub) UpsertGoogleUser(_ context.Context, user User, _ string) (User, error) {
	s.googleUser = user
	return user, nil
}
func (s *storeStub) CreateSession(_ context.Context, _, _ string, hash string, expires time.Time) error {
	s.sessionHash, s.sessionExpires = hash, expires
	return nil
}
func (s *storeStub) UserBySessionHash(context.Context, string) (User, error) {
	return User{}, ErrInvalidSession
}
func (s *storeStub) RevokeSession(context.Context, string) error { return nil }

type googleStub struct{ profile GoogleProfile }

func (googleStub) Enabled() bool                 { return true }
func (googleStub) AuthURL(string, string) string { return "https://accounts.example/auth" }
func (g googleStub) Exchange(context.Context, string, string) (GoogleProfile, error) {
	return g.profile, nil
}

func TestRegisterHashesPassword(t *testing.T) {
	store := &storeStub{}
	service, err := NewService(store, nil, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.Register(context.Background(), " ADA@Example.com ", "password123", " Ada ")
	if err != nil {
		t.Fatal(err)
	}
	if user.Email != "ada@example.com" || store.passwordHash == "password123" {
		t.Fatalf("unexpected created user: %+v", user)
	}
	if bcrypt.CompareHashAndPassword([]byte(store.passwordHash), []byte("password123")) != nil {
		t.Fatal("stored password is not a valid hash")
	}
}

func TestAuthenticateUsesGenericCredentialError(t *testing.T) {
	store := &storeStub{passwordErr: ErrInvalidCredentials}
	service, _ := NewService(store, nil, 24*time.Hour)
	_, err := service.Authenticate(context.Background(), "missing@example.com", "password123")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("expected invalid credentials, got %v", err)
	}
}

func TestNewSessionStoresOnlyTokenHash(t *testing.T) {
	store := &storeStub{}
	service, _ := NewService(store, nil, 24*time.Hour)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	service.now = func() time.Time { return now }
	token, err := service.NewSession(context.Background(), "user-id")
	if err != nil {
		t.Fatal(err)
	}
	if token == store.sessionHash || store.sessionHash != tokenHash(token) {
		t.Fatal("session token was not hashed before storage")
	}
	if !store.sessionExpires.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("unexpected expiry: %s", store.sessionExpires)
	}
}

func TestGoogleRequiresVerifiedEmail(t *testing.T) {
	store := &storeStub{}
	service, _ := NewService(store, googleStub{profile: GoogleProfile{
		Subject: "google-sub", Email: "ada@example.com", Name: "Ada", EmailVerified: false,
	}}, 24*time.Hour)
	_, err := service.AuthenticateGoogle(context.Background(), "code", "verifier")
	if !errors.Is(err, ErrInvalidGoogleUser) {
		t.Fatalf("expected unverified Google account rejection, got %v", err)
	}
}
