package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore persists authentication records in PostgreSQL.
type PostgresStore struct{ pool *pgxpool.Pool }

// NewPostgresStore creates a PostgreSQL authentication store.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

func (s *PostgresStore) CreateUser(ctx context.Context, user User, passwordHash string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_user (id, email, password_hash, display_name, status)
		VALUES ($1, $2, $3, $4, 'active')`,
		user.ID, user.Email, passwordHash, user.DisplayName)
	if uniqueViolation(err) {
		return ErrEmailInUse
	}
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (s *PostgresStore) PasswordUserByEmail(ctx context.Context, email string) (passwordUser, error) {
	var user passwordUser
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, COALESCE(password_hash, '')
		FROM app_user
		WHERE lower(email) = lower($1) AND status = 'active'`, email).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return passwordUser{}, ErrInvalidCredentials
	}
	if err != nil {
		return passwordUser{}, fmt.Errorf("find password user: %w", err)
	}
	return user, nil
}

func (s *PostgresStore) UpsertGoogleUser(ctx context.Context, candidate User, subject string) (User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return User{}, fmt.Errorf("begin Google sign-in: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var user User
	err = tx.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name
		FROM auth_identity i
		JOIN app_user u ON u.id = i.user_id
		WHERE i.provider = 'google' AND i.provider_subject = $1
		  AND u.status = 'active'`, subject).
		Scan(&user.ID, &user.Email, &user.DisplayName)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return User{}, fmt.Errorf("commit Google sign-in: %w", err)
		}
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return User{}, fmt.Errorf("find Google identity: %w", err)
	}

	err = tx.QueryRow(ctx, `
		SELECT id, email, display_name
		FROM app_user
		WHERE lower(email) = lower($1) AND status = 'active'
		FOR UPDATE`, candidate.Email).
		Scan(&user.ID, &user.Email, &user.DisplayName)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		user = candidate
		_, err = tx.Exec(ctx, `
			INSERT INTO app_user (id, email, password_hash, display_name, status, email_verified_at)
			VALUES ($1, $2, NULL, $3, 'active', now())`, user.ID, user.Email, user.DisplayName)
	case err == nil:
		_, err = tx.Exec(ctx, `
			UPDATE app_user SET email_verified_at = COALESCE(email_verified_at, now()), updated_at = now()
			WHERE id = $1`, user.ID)
	}
	if err != nil {
		return User{}, fmt.Errorf("upsert Google user: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO auth_identity (user_id, provider, provider_subject, email)
		VALUES ($1, 'google', $2, $3)`, user.ID, subject, candidate.Email)
	if err != nil {
		return User{}, fmt.Errorf("create Google identity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit Google sign-in: %w", err)
	}
	return user, nil
}

func (s *PostgresStore) CreateSession(ctx context.Context, id, userID, hash string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO user_session (id, user_id, token_hash, expires_at, last_seen_at)
		VALUES ($1, $2, $3, $4, now())`, id, userID, hash, expiresAt)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (s *PostgresStore) UserBySessionHash(ctx context.Context, hash string) (User, error) {
	var user User
	err := s.pool.QueryRow(ctx, `
		WITH active_session AS (
			SELECT user_id
			FROM user_session
			WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		), touch AS (
			UPDATE user_session
			SET last_seen_at = now()
			WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
			  AND (last_seen_at IS NULL OR last_seen_at < now() - interval '5 minutes')
		)
		SELECT u.id, u.email, u.display_name
		FROM active_session s
		JOIN app_user u ON u.id = s.user_id
		WHERE u.status = 'active'`, hash).
		Scan(&user.ID, &user.Email, &user.DisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidSession
	}
	if err != nil {
		return User{}, fmt.Errorf("find session: %w", err)
	}
	return user, nil
}

func (s *PostgresStore) RevokeSession(ctx context.Context, hash string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE user_session SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL`, hash)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
