package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM users LIMIT 1").Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check users: %w", err)
	}
	return true, nil
}

// BootstrapAdmin creates the only MVP administrator or returns the existing one.
func (s *Store) BootstrapAdmin(ctx context.Context, id, username, passwordHash string, now time.Time) (User, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, false, fmt.Errorf("begin admin bootstrap: %w", err)
	}
	defer tx.Rollback()

	user, err := findFirstUser(ctx, tx)
	if err == nil {
		return user, false, tx.Commit()
	}
	if !errors.Is(err, ErrNotFound) {
		return User{}, false, err
	}
	if id == "" || username == "" || passwordHash == "" {
		return User{}, false, errors.New("admin bootstrap fields are required")
	}
	createdAt := now.UTC()
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO users(id, username, password_hash, created_at) VALUES (?, ?, ?, ?)",
		id, username, passwordHash, encodeTime(createdAt)); err != nil {
		return User{}, false, fmt.Errorf("insert bootstrap admin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, false, fmt.Errorf("commit admin bootstrap: %w", err)
	}
	return User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: createdAt}, true, nil
}

func findFirstUser(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (User, error) {
	var user User
	var createdAt string
	err := queryer.QueryRowContext(ctx,
		"SELECT id, username, password_hash, created_at FROM users ORDER BY created_at LIMIT 1").
		Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find first user: %w", err)
	}
	user.CreatedAt, err = decodeTime(createdAt)
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, username, password_hash, created_at FROM users WHERE username = ?", username).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("find user: %w", err)
	}
	user.CreatedAt, err = decodeTime(createdAt)
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) CreateWebSession(ctx context.Context, session WebSession) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_sessions
		(id, user_id, token_digest, expires_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		session.ID, session.UserID, session.TokenDigest, encodeTime(session.ExpiresAt), encodeTime(session.CreatedAt))
	if err != nil {
		return fmt.Errorf("create web session: %w", err)
	}
	return nil
}

func (s *Store) UserBySessionDigest(ctx context.Context, digest []byte, now time.Time) (User, error) {
	var user User
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT users.id, users.username, users.password_hash, users.created_at
		FROM web_sessions JOIN users ON users.id = web_sessions.user_id
		WHERE web_sessions.token_digest = ? AND web_sessions.expires_at > ?`, digest, encodeTime(now)).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("authenticate web session: %w", err)
	}
	user.CreatedAt, err = decodeTime(createdAt)
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) DeleteWebSession(ctx context.Context, digest []byte) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM web_sessions WHERE token_digest = ?", digest); err != nil {
		return fmt.Errorf("delete web session: %w", err)
	}
	return nil
}
