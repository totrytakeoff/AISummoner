package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const deviceUnpairedToolExcerpt = "device unpaired"

type UnpairResult struct {
	RevokedAgentSessionIDs []string
}

// RegisterDevice inserts a device or refreshes mutable metadata when its key matches.
func (s *Store) RegisterDevice(ctx context.Context, device Device) (Device, error) {
	if device.ID == "" || len(device.PublicKey) == 0 || device.Name == "" || device.Platform == "" || device.Arch == "" || device.ClientVersion == "" {
		return Device{}, errors.New("device registration fields are required")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO devices
		(id, public_key, owner_user_id, name, platform, arch, client_version, created_at, paired_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			platform = excluded.platform,
			arch = excluded.arch,
			client_version = excluded.client_version
		WHERE devices.public_key = excluded.public_key`,
		device.ID, device.PublicKey, device.OwnerUserID, device.Name, device.Platform, device.Arch,
		device.ClientVersion, encodeTime(device.CreatedAt), nullableTime(device.PairedAt), nullableTime(device.LastSeenAt))
	if err != nil {
		return Device{}, fmt.Errorf("register device: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Device{}, fmt.Errorf("inspect device registration: %w", err)
	}
	if affected != 1 {
		return Device{}, ErrConflict
	}
	return s.DeviceByID(ctx, device.ID)
}

func (s *Store) DeviceByID(ctx context.Context, deviceID string) (Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx, deviceSelect+" WHERE id = ?", deviceID))
}

func (s *Store) DeviceByOwner(ctx context.Context, ownerUserID, deviceID string) (Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx, deviceSelect+" WHERE id = ? AND owner_user_id = ?", deviceID, ownerUserID))
}

func (s *Store) DevicesByOwner(ctx context.Context, ownerUserID string) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, deviceSelect+" WHERE owner_user_id = ? ORDER BY created_at", ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	devices := make([]Device, 0)
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate devices: %w", err)
	}
	return devices, nil
}

func (s *Store) UpdateDeviceLastSeen(ctx context.Context, deviceID string, at time.Time) error {
	result, err := s.db.ExecContext(ctx, "UPDATE devices SET last_seen_at = ? WHERE id = ?", encodeTime(at), deviceID)
	if err != nil {
		return fmt.Errorf("update device last seen: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect device last seen update: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UnpairDevice(ctx context.Context, ownerUserID, deviceID string, now time.Time) (UnpairResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UnpairResult{}, fmt.Errorf("begin device unpair: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(ctx,
		"SELECT 1 FROM devices WHERE id = ? AND owner_user_id = ?", deviceID, ownerUserID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UnpairResult{}, ErrNotFound
		}
		return UnpairResult{}, fmt.Errorf("authorize device unpair: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `SELECT id FROM agent_sessions
		WHERE user_id = ? AND device_id = ? ORDER BY id`, ownerUserID, deviceID)
	if err != nil {
		return UnpairResult{}, fmt.Errorf("select revoked agent sessions: %w", err)
	}
	sessionIDs := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return UnpairResult{}, fmt.Errorf("scan revoked agent session: %w", err)
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return UnpairResult{}, fmt.Errorf("iterate revoked agent sessions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return UnpairResult{}, fmt.Errorf("close revoked agent sessions: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		"UPDATE pairing_codes SET consumed_at = ? WHERE device_id = ? AND consumed_at IS NULL",
		encodeTime(now), deviceID); err != nil {
		return UnpairResult{}, fmt.Errorf("consume device pairing codes: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_sessions SET state = ?, updated_at = ?
		WHERE user_id = ? AND device_id = ?`,
		AgentSessionRevoked, encodeTime(now), ownerUserID, deviceID); err != nil {
		return UnpairResult{}, fmt.Errorf("revoke device agent sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tool_calls
		SET status = ?, exit_code = NULL, output_excerpt = ?, completed_at = ?
		WHERE status IN ('pending', 'started') AND completed_at IS NULL AND EXISTS (
			SELECT 1 FROM agent_sessions
			WHERE agent_sessions.id = tool_calls.session_id
			AND agent_sessions.user_id = ? AND agent_sessions.device_id = ?
		)`, ToolCallFailed, deviceUnpairedToolExcerpt, encodeTime(now), ownerUserID, deviceID); err != nil {
		return UnpairResult{}, fmt.Errorf("terminalize device agent tools: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE devices SET owner_user_id = NULL, paired_at = NULL WHERE id = ? AND owner_user_id = ?",
		deviceID, ownerUserID)
	if err != nil {
		return UnpairResult{}, fmt.Errorf("unpair device: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return UnpairResult{}, fmt.Errorf("inspect device unpair: %w", err)
	}
	if affected != 1 {
		return UnpairResult{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return UnpairResult{}, fmt.Errorf("commit device unpair: %w", err)
	}
	return UnpairResult{RevokedAgentSessionIDs: sessionIDs}, nil
}

const deviceSelect = `SELECT id, public_key, owner_user_id, name, platform, arch, client_version,
	created_at, paired_at, last_seen_at FROM devices`

type rowScanner interface {
	Scan(...any) error
}

func scanDevice(row rowScanner) (Device, error) {
	var device Device
	var owner sql.NullString
	var created string
	var paired, lastSeen sql.NullString
	if err := row.Scan(&device.ID, &device.PublicKey, &owner, &device.Name, &device.Platform, &device.Arch,
		&device.ClientVersion, &created, &paired, &lastSeen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Device{}, ErrNotFound
		}
		return Device{}, fmt.Errorf("scan device: %w", err)
	}
	if owner.Valid {
		device.OwnerUserID = &owner.String
	}
	var err error
	device.CreatedAt, err = decodeTime(created)
	if err != nil {
		return Device{}, err
	}
	device.PairedAt, err = decodeNullableTime(paired)
	if err != nil {
		return Device{}, err
	}
	device.LastSeenAt, err = decodeNullableTime(lastSeen)
	if err != nil {
		return Device{}, err
	}
	return device, nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return encodeTime(*value)
}
