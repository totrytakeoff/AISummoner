// Package pairing implements short-lived, HMAC-digested one-time pairing codes.
package pairing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/store"
)

const (
	CodeLifetime = 10 * time.Minute
	codeLength   = 8
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var ErrInvalidCode = errors.New("invalid pairing code")

type Store interface {
	CreatePairingCode(context.Context, store.PairingCode, time.Time) error
	ClaimPairing(context.Context, string, []byte, time.Time) (store.Device, error)
}

type Service struct {
	store  Store
	secret []byte
	random io.Reader
}

type Offer struct {
	Code      string
	ExpiresAt time.Time
}

func NewService(store Store, secret []byte) (*Service, error) {
	if len(secret) < 32 {
		return nil, errors.New("pairing secret must contain at least 32 bytes")
	}
	return &Service{store: store, secret: append([]byte(nil), secret...), random: rand.Reader}, nil
}

func (s *Service) Offer(ctx context.Context, deviceID string, now time.Time) (Offer, error) {
	code, err := s.generateCode()
	if err != nil {
		return Offer{}, err
	}
	pairingID, err := id.New("pair")
	if err != nil {
		return Offer{}, err
	}
	expiresAt := now.UTC().Add(CodeLifetime)
	if err := s.store.CreatePairingCode(ctx, store.PairingCode{
		ID: pairingID, DeviceID: deviceID, CodeDigest: s.digest(code), ExpiresAt: expiresAt, CreatedAt: now.UTC(),
	}, now.UTC()); err != nil {
		return Offer{}, err
	}
	return Offer{Code: code[:4] + "-" + code[4:], ExpiresAt: expiresAt}, nil
}

func (s *Service) Claim(ctx context.Context, userID, code string, now time.Time) (store.Device, error) {
	normalized, err := Normalize(code)
	if err != nil {
		return store.Device{}, ErrInvalidCode
	}
	device, err := s.store.ClaimPairing(ctx, userID, s.digest(normalized), now.UTC())
	if errors.Is(err, store.ErrPairingInvalid) {
		return store.Device{}, ErrInvalidCode
	}
	return device, err
}

func Normalize(code string) (string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	if len(normalized) != codeLength {
		return "", ErrInvalidCode
	}
	for _, character := range normalized {
		if !strings.ContainsRune(codeAlphabet, character) {
			return "", ErrInvalidCode
		}
	}
	return normalized, nil
}

func (s *Service) digest(code string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(code))
	return mac.Sum(nil)
}

func (s *Service) generateCode() (string, error) {
	random := make([]byte, codeLength)
	if _, err := io.ReadFull(s.random, random); err != nil {
		return "", fmt.Errorf("read pairing code randomness: %w", err)
	}
	code := make([]byte, codeLength)
	for index, value := range random {
		code[index] = codeAlphabet[int(value)&(len(codeAlphabet)-1)]
	}
	return string(code), nil
}
