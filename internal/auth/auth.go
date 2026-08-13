// Package auth implements local and Google authentication and opaque sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const dummyPasswordHash = "$2a$10$gOv3Wqwn0lv0d3FG07PBj.brtNFzuqHRr9yy7.eeVcQFadXnqMqzC"

var (
	ErrEmailInUse         = errors.New("email is already registered")
	ErrInvalidInput       = errors.New("invalid registration details")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidSession     = errors.New("invalid session")
	ErrInvalidGoogleUser  = errors.New("google account could not be verified")
)

// User is the authenticated application identity exposed to the web layer.
type User struct {
	ID          string
	Email       string
	DisplayName string
}

type passwordUser struct {
	User
	PasswordHash string
}

// Store persists users, identities, and sessions.
type Store interface {
	CreateUser(context.Context, User, string) error
	PasswordUserByEmail(context.Context, string) (passwordUser, error)
	UpsertGoogleUser(context.Context, User, string) (User, error)
	CreateSession(context.Context, string, string, string, time.Time) error
	UserBySessionHash(context.Context, string) (User, error)
	RevokeSession(context.Context, string) error
}

// GoogleProvider performs Google's OAuth flow.
type GoogleProvider interface {
	Enabled() bool
	AuthURL(state, codeChallenge string) string
	Exchange(context.Context, string, string) (GoogleProfile, error)
}

// GoogleProfile contains the verified identity returned by Google.
type GoogleProfile struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"email_verified"`
}

// Service coordinates password auth, OAuth identities, and sessions.
type Service struct {
	store           Store
	google          GoogleProvider
	sessionDuration time.Duration
	now             func() time.Time
}

// NewService creates an authentication service.
func NewService(store Store, google GoogleProvider, sessionDuration time.Duration) (*Service, error) {
	if store == nil || sessionDuration <= 0 {
		return nil, fmt.Errorf("auth store and positive session duration are required")
	}
	return &Service{store: store, google: google, sessionDuration: sessionDuration, now: time.Now}, nil
}

// Register creates a local password user.
func (s *Service) Register(ctx context.Context, email, password, displayName string) (User, error) {
	email, displayName, err := validateRegistration(email, password, displayName)
	if err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	id, err := newUUID()
	if err != nil {
		return User{}, err
	}
	user := User{ID: id, Email: email, DisplayName: displayName}
	if err := s.store.CreateUser(ctx, user, string(hash)); err != nil {
		return User{}, err
	}
	return user, nil
}

// Authenticate checks a local user's password. Its error intentionally does not
// reveal whether an email exists.
func (s *Service) Authenticate(ctx context.Context, email, password string) (User, error) {
	email = normalizeEmail(email)
	if email == "" || password == "" {
		return User{}, ErrInvalidCredentials
	}
	user, err := s.store.PasswordUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, ErrInvalidCredentials) {
			return User{}, err
		}
		// Keep the unknown-user path computationally similar to a real login.
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if user.PasswordHash == "" {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyPasswordHash), []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return user.User, nil
}

// GoogleEnabled reports whether Google OAuth has complete configuration.
func (s *Service) GoogleEnabled() bool { return s.google != nil && s.google.Enabled() }

// GoogleAuthURL creates Google's authorization URL.
func (s *Service) GoogleAuthURL(state, codeChallenge string) string {
	if !s.GoogleEnabled() {
		return ""
	}
	return s.google.AuthURL(state, codeChallenge)
}

// AuthenticateGoogle exchanges a callback code and finds or creates its user.
func (s *Service) AuthenticateGoogle(ctx context.Context, code, verifier string) (User, error) {
	if !s.GoogleEnabled() || code == "" || verifier == "" {
		return User{}, ErrInvalidGoogleUser
	}
	profile, err := s.google.Exchange(ctx, code, verifier)
	if err != nil || profile.Subject == "" || !profile.EmailVerified {
		return User{}, ErrInvalidGoogleUser
	}
	email := normalizeEmail(profile.Email)
	if !validEmail(email) {
		return User{}, ErrInvalidGoogleUser
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	if characters := []rune(name); len(characters) > 80 {
		name = string(characters[:80])
	}
	id, err := newUUID()
	if err != nil {
		return User{}, err
	}
	user := User{ID: id, Email: email, DisplayName: name}
	return s.store.UpsertGoogleUser(ctx, user, profile.Subject)
}

// NewSession persists a new opaque session and returns the cookie token.
func (s *Service) NewSession(ctx context.Context, userID string) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	id, err := newUUID()
	if err != nil {
		return "", err
	}
	expires := s.now().UTC().Add(s.sessionDuration)
	if err := s.store.CreateSession(ctx, id, userID, tokenHash(token), expires); err != nil {
		return "", err
	}
	return token, nil
}

// UserBySession resolves an opaque cookie token.
func (s *Service) UserBySession(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrInvalidSession
	}
	return s.store.UserBySessionHash(ctx, tokenHash(token))
}

// RevokeSession invalidates the session represented by token.
func (s *Service) RevokeSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.RevokeSession(ctx, tokenHash(token))
}

// SessionDuration is used by the web layer to set the cookie lifetime.
func (s *Service) SessionDuration() time.Duration { return s.sessionDuration }

// RandomToken returns a URL-safe random value for CSRF, OAuth state, and PKCE.
func RandomToken(bytes int) (string, error) { return randomToken(bytes) }

// PKCEChallenge computes an RFC 7636 S256 challenge.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64RawURL(sum[:])
}

func validateRegistration(email, password, displayName string) (string, string, error) {
	email = normalizeEmail(email)
	displayName = strings.TrimSpace(displayName)
	nameLength := len([]rune(displayName))
	if !validEmail(email) || nameLength < 2 || nameLength > 80 || len(password) < 8 || len(password) > 72 {
		return "", "", ErrInvalidInput
	}
	return email, displayName, nil
}

func validEmail(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email && strings.Contains(email, "@")
}

func normalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64RawURL(value), nil
}

func base64RawURL(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}

func newUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}
