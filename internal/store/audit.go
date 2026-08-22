package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *Store) CreateAuditEvent(ctx context.Context, event AuditEvent) error {
	if event.ID == "" || event.EventType == "" || !json.Valid([]byte(event.MetadataJSON)) {
		return errors.New("invalid audit event")
	}
	if len(event.MetadataJSON) > 8192 {
		return errors.New("audit event metadata is too large")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_events
		(id, actor_user_id, device_id, event_type, metadata_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		event.ID, event.ActorUserID, event.DeviceID, event.EventType, event.MetadataJSON, encodeTime(event.CreatedAt))
	if err != nil {
		return fmt.Errorf("create audit event: %w", err)
	}
	return nil
}
