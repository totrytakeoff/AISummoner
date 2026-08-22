// Package auth implements single-administrator bootstrap and opaque web sessions.
package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/store"
)

const SessionDuration = 24 * time.Hour

const passwordVerificationCapacity = 2

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrVerificationBusy   = errors.New("password verification capacity exhausted")
	ErrVerificationFailed = errors.New("password verification failed")

	passwordVerificationSlots = make(chan struct{}, passwordVerificationCapacity)
)

type passwordVerifier func(string, string) (bool, error)

type Store interface {
	HasUsers(context.Context) (bool, error)
	BootstrapAdmin(context.Context, string, string, string, time.Time) (store.User, bool, error)
	UserByUsername(context.Context, string) (store.User, error)
	CreateWebSession(context.Context, store.WebSession) error
	UserBySessionDigest(context.Context, []byte, time.Time) (store.User, error)
	DeleteWebSession(context.Context, []byte) error
}

type Service struct {
	store          Store
	verifyPassword passwordVerifier
}

func NewService(store Store) *Service {
	return &Service{store: store, verifyPassword: VerifyPassword}
}

// Bootstrap creates exactly one admin. An existing installation ignores password.
func (s *Service) Bootstrap(ctx context.Context, password string, now time.Time) (store.User, bool, error) {
	hasUsers, err := s.store.HasUsers(ctx)
	if err != nil {
		return store.User{}, false, err
	}
	if hasUsers {
		user, err := s.store.UserByUsername(ctx, "admin")
		return user, false, err
	}
	if password == "" {
		return store.User{}, false, errors.New("AISUMMONER_ADMIN_PASSWORD is required on first start")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return store.User{}, false, err
	}
	userID, err := id.New("usr")
	if err != nil {
		return store.User{}, false, err
	}
	return s.store.BootstrapAdmin(ctx, userID, "admin", hash, now)
}

type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	User      store.User
}

func (s *Service) Login(ctx context.Context, username, password string, now time.Time) (LoginResult, error) {
	if err := ctx.Err(); err != nil {
		return LoginResult{}, err
	}
	if len(username) == 0 || len(username) > 128 || len(password) == 0 || len(password) > 1024 {
		return LoginResult{}, ErrInvalidCredentials
	}
	user, err := s.store.UserByUsername(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, err
	}
	valid, err := s.verifyPasswordWithAdmission(ctx, user.PasswordHash, password)
	if err != nil {
		if errors.Is(err, ErrVerificationBusy) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return LoginResult{}, err
		}
		return LoginResult{}, ErrVerificationFailed
	}
	if !valid {
		return LoginResult{}, ErrInvalidCredentials
	}
	token, err := id.Token(32)
	if err != nil {
		return LoginResult{}, err
	}
	sessionID, err := id.New("ses")
	if err != nil {
		return LoginResult{}, err
	}
	expiresAt := now.UTC().Add(SessionDuration)
	digest := DigestToken(token)
	if err := s.store.CreateWebSession(ctx, store.WebSession{
		ID: sessionID, UserID: user.ID, TokenDigest: digest, ExpiresAt: expiresAt, CreatedAt: now.UTC(),
	}); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, ExpiresAt: expiresAt, User: user}, nil
}

func (s *Service) verifyPasswordWithAdmission(ctx context.Context, encodedHash, password string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	select {
	case passwordVerificationSlots <- struct{}{}:
		defer func() { <-passwordVerificationSlots }()
	default:
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return false, ErrVerificationBusy
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return s.verifyPassword(encodedHash, password)
}

func (s *Service) Authenticate(ctx context.Context, token string, now time.Time) (store.User, error) {
	if len(token) < 20 || len(token) > 256 {
		return store.User{}, ErrInvalidCredentials
	}
	user, err := s.store.UserBySessionDigest(ctx, DigestToken(token), now.UTC())
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrInvalidCredentials
	}
	return user, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" || len(token) > 256 {
		return nil
	}
	return s.store.DeleteWebSession(ctx, DigestToken(token))
}

func DigestToken(token string) []byte {
	digest := sha256.Sum256([]byte(token))
	return digest[:]
}
