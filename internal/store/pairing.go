package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreatePairingCode replaces any active code for an unowned device.
func (s *Store) CreatePairingCode(ctx context.Context, pairing PairingCode, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pairing code creation: %w", err)
	}
	defer tx.Rollback()
	var owner sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT owner_user_id FROM devices WHERE id = ?", pairing.DeviceID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("find pairing device: %w", err)
	}
	if owner.Valid {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE pairing_codes SET consumed_at = ? WHERE device_id = ? AND consumed_at IS NULL",
		encodeTime(now), pairing.DeviceID); err != nil {
		return fmt.Errorf("replace active pairing code: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pairing_codes
		(id, device_id, code_digest, expires_at, consumed_at, created_at) VALUES (?, ?, ?, ?, NULL, ?)`,
		pairing.ID, pairing.DeviceID, pairing.CodeDigest, encodeTime(pairing.ExpiresAt), encodeTime(pairing.CreatedAt)); err != nil {
		return fmt.Errorf("create pairing code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pairing code creation: %w", err)
	}
	return nil
}

// ClaimPairing atomically binds the device and consumes the one-time code.
func (s *Store) ClaimPairing(ctx context.Context, userID string, digest []byte, now time.Time) (Device, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, fmt.Errorf("begin pairing claim: %w", err)
	}
	defer tx.Rollback()

	var pairingID, deviceID, expiresAt string
	var consumedAt sql.NullString
	err = tx.QueryRowContext(ctx,
		"SELECT id, device_id, expires_at, consumed_at FROM pairing_codes WHERE code_digest = ?", digest).
		Scan(&pairingID, &deviceID, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) || consumedAt.Valid {
		return Device{}, ErrPairingInvalid
	}
	if err != nil {
		return Device{}, fmt.Errorf("find pairing code: %w", err)
	}
	expires, err := decodeTime(expiresAt)
	if err != nil {
		return Device{}, err
	}
	if !now.Before(expires) {
		return Device{}, ErrPairingExpired
	}
	pairedAt := encodeTime(now)
	result, err := tx.ExecContext(ctx,
		"UPDATE devices SET owner_user_id = ?, paired_at = ? WHERE id = ? AND owner_user_id IS NULL",
		userID, pairedAt, deviceID)
	if err != nil {
		return Device{}, fmt.Errorf("bind pairing device: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Device{}, fmt.Errorf("inspect pairing device binding: %w", err)
	}
	if affected != 1 {
		return Device{}, ErrConflict
	}
	result, err = tx.ExecContext(ctx,
		"UPDATE pairing_codes SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL", pairedAt, pairingID)
	if err != nil {
		return Device{}, fmt.Errorf("consume pairing code: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return Device{}, fmt.Errorf("inspect pairing code consumption: %w", err)
	}
	if affected != 1 {
		return Device{}, ErrPairingInvalid
	}
	device, err := scanDevice(tx.QueryRowContext(ctx, deviceSelect+" WHERE id = ?", deviceID))
	if err != nil {
		return Device{}, err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, fmt.Errorf("commit pairing claim: %w", err)
	}
	return device, nil
}
