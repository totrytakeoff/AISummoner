package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	AgentApprovalPerCommand = "per_command"
	AgentApprovalFullAccess = "full_access"

	AgentSessionIdle            = "idle"
	AgentSessionRunning         = "running"
	AgentSessionWaitingApproval = "waiting_approval"
	AgentSessionFailed          = "failed"
	AgentSessionRevoked         = "revoked"

	ToolCallPending   = "pending"
	ToolCallStarted   = "started"
	ToolCallCompleted = "completed"
	ToolCallDenied    = "denied"
	ToolCallFailed    = "failed"

	recentAgentSessionLimit = 50
	agentSessionTitleRunes  = 80
)

const agentSessionOwnershipPredicate = `agent_sessions.user_id = ?
	AND agent_sessions.state <> 'revoked'
	AND EXISTS (
		SELECT 1 FROM devices
		WHERE devices.id = agent_sessions.device_id
		AND devices.owner_user_id = agent_sessions.user_id
	)`

const agentSessionOwnerPredicate = agentSessionOwnershipPredicate + `
	AND agent_sessions.archived_at IS NULL`

const agentToolOwnerPredicate = `EXISTS (
	SELECT 1 FROM agent_sessions JOIN devices ON devices.id = agent_sessions.device_id
		WHERE agent_sessions.id = tool_calls.session_id
		AND agent_sessions.user_id = ?
		AND agent_sessions.state <> 'revoked'
		AND agent_sessions.archived_at IS NULL
		AND devices.owner_user_id = agent_sessions.user_id
)`

type agentQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// AgentSession is the persisted product session. ExternalSessionID belongs to
// the configured provider; it never controls the user or Device association.
type AgentSession struct {
	ID                string
	UserID            string
	DeviceID          string
	ApprovalMode      string
	Provider          string
	ExternalSessionID *string
	State             string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ArchivedAt        *time.Time
}

type AgentMessage struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	CreatedAt time.Time
}

type ToolCall struct {
	ID            string
	SessionID     string
	Name          string
	ArgumentsJSON string
	Status        string
	Decision      *string
	ExitCode      *int
	OutputExcerpt *string
	CreatedAt     time.Time
	CompletedAt   *time.Time
}

type AgentSnapshot struct {
	Session   AgentSession
	Messages  []AgentMessage
	ToolCalls []ToolCall
}

// AgentSessionSummary is the bounded Controller index projection. Title is
// derived from the first user message and never includes provider credentials
// or ExternalSessionID.
type AgentSessionSummary struct {
	ID           string
	DeviceID     string
	DeviceName   string
	ApprovalMode string
	Provider     string
	State        string
	Title        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   *time.Time
}

type AgentSettings struct {
	DefaultApprovalMode string
	UpdatedAt           *time.Time
}

// CreateAgentSession uses an INSERT ... SELECT ownership check, so a caller
// cannot race an owner lookup and bind a Session to somebody else's Device.
func (s *Store) CreateAgentSession(ctx context.Context, session AgentSession) error {
	if session.ID == "" || session.UserID == "" || session.DeviceID == "" || session.Provider == "" ||
		!validApprovalMode(session.ApprovalMode) || !validAgentSessionState(session.State) {
		return errors.New("invalid agent session")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO agent_sessions
		(id, user_id, device_id, approval_mode, provider, external_session_id, state, created_at, updated_at)
		SELECT ?, ?, devices.id, ?, ?, ?, ?, ?, ? FROM devices
		WHERE devices.id = ? AND devices.owner_user_id = ?`,
		session.ID, session.UserID, session.ApprovalMode, session.Provider, session.ExternalSessionID,
		session.State, encodeTime(session.CreatedAt), encodeTime(session.UpdatedAt), session.DeviceID, session.UserID)
	if err != nil {
		return fmt.Errorf("create agent session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect agent session creation: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AgentSessionByOwner(ctx context.Context, ownerUserID, sessionID string) (AgentSession, error) {
	return agentSessionByOwnerQuery(ctx, s.db, ownerUserID, sessionID)
}

func (s *Store) AgentSnapshotByOwner(ctx context.Context, ownerUserID, sessionID string) (AgentSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AgentSnapshot{}, fmt.Errorf("begin agent snapshot: %w", err)
	}
	defer tx.Rollback()
	session, err := agentSessionByOwnerQuery(ctx, tx, ownerUserID, sessionID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	messages, err := agentMessagesByOwnerQuery(ctx, tx, ownerUserID, sessionID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	toolCalls, err := agentToolCallsByOwnerQuery(ctx, tx, ownerUserID, sessionID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentSnapshot{}, fmt.Errorf("commit agent snapshot: %w", err)
	}
	return AgentSnapshot{Session: session, Messages: messages, ToolCalls: toolCalls}, nil
}

// LatestAgentSnapshotByDeviceOwner returns the newest non-revoked Session for
// a Device while rechecking both the Session owner and the Device's current
// owner in one read transaction.
func (s *Store) LatestAgentSnapshotByDeviceOwner(ctx context.Context, ownerUserID, deviceID string) (AgentSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AgentSnapshot{}, fmt.Errorf("begin latest agent snapshot: %w", err)
	}
	defer tx.Rollback()
	session, err := scanAgentSession(tx.QueryRowContext(ctx, agentSessionSelect+`
		WHERE agent_sessions.user_id = ? AND agent_sessions.device_id = ? AND `+agentSessionOwnerPredicate+`
		ORDER BY agent_sessions.created_at DESC, agent_sessions.id DESC LIMIT 1`, ownerUserID, deviceID, ownerUserID))
	if err != nil {
		return AgentSnapshot{}, err
	}
	messages, err := agentMessagesByOwnerQuery(ctx, tx, ownerUserID, session.ID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	toolCalls, err := agentToolCallsByOwnerQuery(ctx, tx, ownerUserID, session.ID)
	if err != nil {
		return AgentSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentSnapshot{}, fmt.Errorf("commit latest agent snapshot: %w", err)
	}
	return AgentSnapshot{Session: session, Messages: messages, ToolCalls: toolCalls}, nil
}

// RecentAgentSessionsByDeviceOwner returns a fixed-size, newest-first index.
// The Device-owner and non-revoked predicates are evaluated in the same SQL
// statement, so an unowned or unknown Device is indistinguishable from one
// with no visible Sessions.
func (s *Store) RecentAgentSessionsByDeviceOwner(ctx context.Context, ownerUserID, deviceID string) ([]AgentSessionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_sessions.id, agent_sessions.device_id, devices.name,
		agent_sessions.approval_mode, agent_sessions.provider, agent_sessions.state,
		agent_sessions.created_at, agent_sessions.updated_at, agent_sessions.archived_at,
		COALESCE((
			SELECT substr(agent_messages.content, 1, 512)
			FROM agent_messages
			WHERE agent_messages.session_id = agent_sessions.id AND agent_messages.role = 'user'
			ORDER BY agent_messages.created_at, agent_messages.id LIMIT 1
		), '')
		FROM agent_sessions JOIN devices ON devices.id = agent_sessions.device_id
		WHERE agent_sessions.user_id = ? AND agent_sessions.device_id = ? AND `+agentSessionOwnerPredicate+`
		ORDER BY agent_sessions.updated_at DESC, agent_sessions.created_at DESC, agent_sessions.id DESC
		LIMIT ?`, ownerUserID, deviceID, ownerUserID, recentAgentSessionLimit)
	if err != nil {
		return nil, fmt.Errorf("list recent agent sessions: %w", err)
	}
	defer rows.Close()
	values := make([]AgentSessionSummary, 0)
	for rows.Next() {
		var summary AgentSessionSummary
		var createdAt, updatedAt, title string
		var archivedAt sql.NullString
		if err := rows.Scan(&summary.ID, &summary.DeviceID, &summary.DeviceName, &summary.ApprovalMode,
			&summary.Provider, &summary.State, &createdAt, &updatedAt, &archivedAt, &title); err != nil {
			return nil, fmt.Errorf("scan recent agent session: %w", err)
		}
		summary.CreatedAt, err = decodeTime(createdAt)
		if err != nil {
			return nil, err
		}
		summary.UpdatedAt, err = decodeTime(updatedAt)
		if err != nil {
			return nil, err
		}
		summary.Title = normalizeAgentSessionTitle(title)
		summary.ArchivedAt, err = decodeNullableTime(archivedAt)
		if err != nil {
			return nil, err
		}
		values = append(values, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent agent sessions: %w", err)
	}
	return values, nil
}

// ArchivedAgentSessionsByOwner returns a bounded newest-first management view.
// It never exposes sessions after Device ownership is removed.
func (s *Store) ArchivedAgentSessionsByOwner(ctx context.Context, ownerUserID string) ([]AgentSessionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT agent_sessions.id, agent_sessions.device_id, devices.name,
		agent_sessions.approval_mode, agent_sessions.provider, agent_sessions.state,
		agent_sessions.created_at, agent_sessions.updated_at, agent_sessions.archived_at,
		COALESCE((
			SELECT substr(agent_messages.content, 1, 512)
			FROM agent_messages
			WHERE agent_messages.session_id = agent_sessions.id AND agent_messages.role = 'user'
			ORDER BY agent_messages.created_at, agent_messages.id LIMIT 1
		), '')
		FROM agent_sessions JOIN devices ON devices.id = agent_sessions.device_id
		WHERE agent_sessions.archived_at IS NOT NULL AND `+agentSessionOwnershipPredicate+`
		ORDER BY agent_sessions.archived_at DESC, agent_sessions.updated_at DESC, agent_sessions.id DESC
		LIMIT ?`, ownerUserID, recentAgentSessionLimit)
	if err != nil {
		return nil, fmt.Errorf("list archived agent sessions: %w", err)
	}
	defer rows.Close()
	values := make([]AgentSessionSummary, 0)
	for rows.Next() {
		var summary AgentSessionSummary
		var createdAt, updatedAt, title string
		var archivedAt sql.NullString
		if err := rows.Scan(&summary.ID, &summary.DeviceID, &summary.DeviceName, &summary.ApprovalMode,
			&summary.Provider, &summary.State, &createdAt, &updatedAt, &archivedAt, &title); err != nil {
			return nil, fmt.Errorf("scan archived agent session: %w", err)
		}
		var decodeErr error
		summary.CreatedAt, decodeErr = decodeTime(createdAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		summary.UpdatedAt, decodeErr = decodeTime(updatedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		summary.ArchivedAt, decodeErr = decodeNullableTime(archivedAt)
		if decodeErr != nil {
			return nil, decodeErr
		}
		summary.Title = normalizeAgentSessionTitle(title)
		values = append(values, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate archived agent sessions: %w", err)
	}
	return values, nil
}

func (s *Store) AgentSettingsByOwner(ctx context.Context, ownerUserID string) (AgentSettings, error) {
	var settings AgentSettings
	var updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT agent_user_settings.default_approval_mode,
		agent_user_settings.updated_at FROM agent_user_settings JOIN users
		ON users.id = agent_user_settings.user_id WHERE users.id = ?`, ownerUserID).
		Scan(&settings.DefaultApprovalMode, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		var exists int
		if lookupErr := s.db.QueryRowContext(ctx, "SELECT 1 FROM users WHERE id = ?", ownerUserID).Scan(&exists); lookupErr != nil {
			if errors.Is(lookupErr, sql.ErrNoRows) {
				return AgentSettings{}, ErrNotFound
			}
			return AgentSettings{}, fmt.Errorf("look up agent settings owner: %w", lookupErr)
		}
		return AgentSettings{DefaultApprovalMode: AgentApprovalPerCommand}, nil
	}
	if err != nil {
		return AgentSettings{}, fmt.Errorf("get agent settings: %w", err)
	}
	var decodeErr error
	settings.UpdatedAt, decodeErr = decodeNullableTime(updatedAt)
	if decodeErr != nil {
		return AgentSettings{}, decodeErr
	}
	return settings, nil
}

func (s *Store) UpdateAgentSettings(ctx context.Context, ownerUserID, approvalMode string, now time.Time) (AgentSettings, error) {
	if !validApprovalMode(approvalMode) {
		return AgentSettings{}, errors.New("invalid default agent approval mode")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO agent_user_settings(user_id, default_approval_mode, updated_at)
		SELECT users.id, ?, ? FROM users WHERE users.id = ?
		ON CONFLICT(user_id) DO UPDATE SET default_approval_mode = excluded.default_approval_mode,
		updated_at = excluded.updated_at`, approvalMode, encodeTime(now), ownerUserID)
	if err != nil {
		return AgentSettings{}, fmt.Errorf("update agent settings: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return AgentSettings{}, err
	}
	return s.AgentSettingsByOwner(ctx, ownerUserID)
}

func (s *Store) UpdateAgentSessionApprovalMode(ctx context.Context, ownerUserID, sessionID, approvalMode string, now time.Time) (AgentSession, error) {
	if !validApprovalMode(approvalMode) {
		return AgentSession{}, errors.New("invalid agent approval mode")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET approval_mode = ?, updated_at = ?
		WHERE id = ? AND state IN ('idle', 'failed') AND `+agentSessionOwnerPredicate,
		approvalMode, encodeTime(now), sessionID, ownerUserID)
	if err != nil {
		return AgentSession{}, fmt.Errorf("update agent approval mode: %w", err)
	}
	if affected, inspectErr := result.RowsAffected(); inspectErr != nil {
		return AgentSession{}, fmt.Errorf("inspect agent approval update: %w", inspectErr)
	} else if affected != 1 {
		if _, lookupErr := s.AgentSessionByOwner(ctx, ownerUserID, sessionID); lookupErr != nil {
			return AgentSession{}, lookupErr
		}
		return AgentSession{}, ErrConflict
	}
	return s.AgentSessionByOwner(ctx, ownerUserID, sessionID)
}

func (s *Store) SetAgentSessionArchived(ctx context.Context, ownerUserID, sessionID string, archived bool, now time.Time) (AgentSession, error) {
	var archivedAt any
	if archived {
		archivedAt = encodeTime(now)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET archived_at = ?, updated_at = ?
		WHERE id = ? AND state IN ('idle', 'failed') AND `+agentSessionOwnershipPredicate,
		archivedAt, encodeTime(now), sessionID, ownerUserID)
	if err != nil {
		return AgentSession{}, fmt.Errorf("set agent session archived: %w", err)
	}
	if affected, inspectErr := result.RowsAffected(); inspectErr != nil {
		return AgentSession{}, fmt.Errorf("inspect agent archive update: %w", inspectErr)
	} else if affected != 1 {
		if _, lookupErr := agentSessionByManagingOwnerQuery(ctx, s.db, ownerUserID, sessionID); lookupErr != nil {
			return AgentSession{}, lookupErr
		}
		return AgentSession{}, ErrConflict
	}
	return agentSessionByManagingOwnerQuery(ctx, s.db, ownerUserID, sessionID)
}

func (s *Store) DeleteAgentSessionByOwner(ctx context.Context, ownerUserID, sessionID string) (AgentSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentSession{}, fmt.Errorf("begin agent session deletion: %w", err)
	}
	defer tx.Rollback()
	session, err := agentSessionByManagingOwnerQuery(ctx, tx, ownerUserID, sessionID)
	if err != nil {
		return AgentSession{}, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM agent_sessions
		WHERE id = ? AND state IN ('idle', 'failed') AND `+agentSessionOwnershipPredicate,
		sessionID, ownerUserID)
	if err != nil {
		return AgentSession{}, fmt.Errorf("delete agent session: %w", err)
	}
	if affected, inspectErr := result.RowsAffected(); inspectErr != nil {
		return AgentSession{}, fmt.Errorf("inspect agent session deletion: %w", inspectErr)
	} else if affected != 1 {
		return AgentSession{}, ErrConflict
	}
	if err := tx.Commit(); err != nil {
		return AgentSession{}, fmt.Errorf("commit agent session deletion: %w", err)
	}
	return session, nil
}

func normalizeAgentSessionTitle(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "New conversation"
	}
	runes := []rune(value)
	if len(runes) <= agentSessionTitleRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:agentSessionTitleRunes-1])) + "…"
}

func (s *Store) UpdateAgentSessionState(ctx context.Context, ownerUserID, sessionID, state string, now time.Time) error {
	if !validAgentSessionState(state) {
		return errors.New("invalid agent session state")
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE agent_sessions SET state = ?, updated_at = ? WHERE id = ? AND "+agentSessionOwnerPredicate,
		state, encodeTime(now), sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("update agent session state: %w", err)
	}
	return requireOneRow(result)
}

// BeginAgentTurn atomically moves an idle/failed Session to running. This is
// the persistent one-running-Turn guard used in addition to the in-memory
// cancellation registry.
func (s *Store) BeginAgentTurn(ctx context.Context, ownerUserID, sessionID string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET state = ?, updated_at = ?
		WHERE id = ? AND state IN ('idle', 'failed') AND `+agentSessionOwnerPredicate,
		AgentSessionRunning, encodeTime(now), sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("begin agent turn: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect agent turn start: %w", err)
	}
	if affected != 1 {
		if _, lookupErr := s.AgentSessionByOwner(ctx, ownerUserID, sessionID); lookupErr != nil {
			return lookupErr
		}
		return ErrConflict
	}
	return nil
}

func (s *Store) UpdateAgentExternalSessionID(ctx context.Context, ownerUserID, sessionID, externalSessionID string, now time.Time) error {
	if externalSessionID == "" || len(externalSessionID) > 512 {
		return errors.New("invalid external agent session id")
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE agent_sessions SET external_session_id = ?, updated_at = ? WHERE id = ? AND "+agentSessionOwnerPredicate,
		externalSessionID, encodeTime(now), sessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("update external agent session id: %w", err)
	}
	return requireOneRow(result)
}

func (s *Store) CreateAgentMessage(ctx context.Context, ownerUserID string, message AgentMessage) error {
	if message.ID == "" || message.SessionID == "" ||
		(message.Role != "user" && message.Role != "assistant" && message.Role != "reasoning") || message.Content == "" {
		return errors.New("invalid agent message")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO agent_messages(id, session_id, role, content, created_at)
		SELECT ?, agent_sessions.id, ?, ?, ? FROM agent_sessions
		WHERE agent_sessions.id = ? AND `+agentSessionOwnerPredicate,
		message.ID, message.Role, message.Content, encodeTime(message.CreatedAt), message.SessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("create agent message: %w", err)
	}
	return requireOneRow(result)
}

func (s *Store) CreateAgentToolCall(ctx context.Context, ownerUserID string, toolCall ToolCall) error {
	if toolCall.ID == "" || toolCall.SessionID == "" || toolCall.Name == "" || toolCall.ArgumentsJSON == "" ||
		(toolCall.Status != ToolCallPending && toolCall.Status != ToolCallStarted) {
		return errors.New("invalid agent tool call")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO tool_calls
		(id, session_id, name, arguments_json, status, decision, exit_code, output_excerpt, created_at, completed_at)
		SELECT ?, agent_sessions.id, ?, ?, ?, NULL, NULL, NULL, ?, NULL FROM agent_sessions
		WHERE agent_sessions.id = ? AND `+agentSessionOwnerPredicate,
		toolCall.ID, toolCall.Name, toolCall.ArgumentsJSON, toolCall.Status,
		encodeTime(toolCall.CreatedAt), toolCall.SessionID, ownerUserID)
	if err != nil {
		return fmt.Errorf("create agent tool call: %w", err)
	}
	return requireOneRow(result)
}

func (s *Store) AgentToolCallByOwner(ctx context.Context, ownerUserID, toolCallID string) (ToolCall, error) {
	return agentToolCallByOwnerQuery(ctx, s.db, ownerUserID, toolCallID)
}

// DecideAgentToolCall atomically records the single decision and, for
// approve_session only, upgrades exactly the owning product Session.
func (s *Store) DecideAgentToolCall(ctx context.Context, ownerUserID, toolCallID, decision string, now time.Time) (ToolCall, AgentSession, error) {
	if decision != "approve_once" && decision != "approve_session" && decision != "deny" {
		return ToolCall{}, AgentSession{}, errors.New("invalid tool decision")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ToolCall{}, AgentSession{}, fmt.Errorf("begin tool decision: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE tool_calls SET decision = ? WHERE id = ? AND status = ? AND decision IS NULL
		AND `+agentToolOwnerPredicate,
		decision, toolCallID, ToolCallPending, ownerUserID)
	if err != nil {
		return ToolCall{}, AgentSession{}, fmt.Errorf("record tool decision: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		if _, lookupErr := agentToolCallByOwnerQuery(ctx, tx, ownerUserID, toolCallID); lookupErr != nil {
			return ToolCall{}, AgentSession{}, lookupErr
		}
		return ToolCall{}, AgentSession{}, ErrConflict
	}
	toolCall, err := agentToolCallByOwnerQuery(ctx, tx, ownerUserID, toolCallID)
	if err != nil {
		return ToolCall{}, AgentSession{}, err
	}
	if decision == "approve_session" {
		upgrade, err := tx.ExecContext(ctx,
			"UPDATE agent_sessions SET approval_mode = ?, updated_at = ? WHERE id = ? AND "+agentSessionOwnerPredicate,
			AgentApprovalFullAccess, encodeTime(now), toolCall.SessionID, ownerUserID)
		if err != nil {
			return ToolCall{}, AgentSession{}, fmt.Errorf("upgrade agent session approval: %w", err)
		}
		if err := requireOneRow(upgrade); err != nil {
			return ToolCall{}, AgentSession{}, err
		}
	}
	toolCall, err = agentToolCallByOwnerQuery(ctx, tx, ownerUserID, toolCallID)
	if err != nil {
		return ToolCall{}, AgentSession{}, err
	}
	session, err := agentSessionByOwnerQuery(ctx, tx, ownerUserID, toolCall.SessionID)
	if err != nil {
		return ToolCall{}, AgentSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return ToolCall{}, AgentSession{}, fmt.Errorf("commit tool decision: %w", err)
	}
	return toolCall, session, nil
}

// FailPendingAgentToolCall atomically makes timeout/cancellation the durable
// winner only while no user decision has been recorded. It contends with
// DecideAgentToolCall on the same pending/decision predicate, so exactly one
// side can succeed and approve_session can never be applied after this wins.
func (s *Store) FailPendingAgentToolCall(ctx context.Context, ownerUserID, toolCallID string, outputExcerpt string, now time.Time) (ToolCall, error) {
	if len([]byte(outputExcerpt)) > 8192 {
		return ToolCall{}, errors.New("tool output excerpt exceeds 8192 bytes")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tool_calls
		SET status = ?, output_excerpt = ?, completed_at = ?
		WHERE id = ? AND status = ? AND decision IS NULL
		AND `+agentToolOwnerPredicate,
		ToolCallFailed, outputExcerpt, encodeTime(now), toolCallID, ToolCallPending, ownerUserID)
	if err != nil {
		return ToolCall{}, fmt.Errorf("fail pending agent tool call: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		existing, lookupErr := s.AgentToolCallByOwner(ctx, ownerUserID, toolCallID)
		if lookupErr != nil {
			return ToolCall{}, lookupErr
		}
		return existing, ErrConflict
	}
	return s.AgentToolCallByOwner(ctx, ownerUserID, toolCallID)
}

func (s *Store) StartAgentToolCall(ctx context.Context, ownerUserID, toolCallID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE tool_calls SET status = ?
		WHERE id = ? AND status = ? AND decision IN ('approve_once', 'approve_session')
		AND `+agentToolOwnerPredicate,
		ToolCallStarted, toolCallID, ToolCallPending, ownerUserID)
	if err != nil {
		return fmt.Errorf("start agent tool call: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		if _, lookupErr := s.AgentToolCallByOwner(ctx, ownerUserID, toolCallID); lookupErr != nil {
			return lookupErr
		}
		return ErrConflict
	}
	return nil
}

func (s *Store) FinishAgentToolCall(ctx context.Context, ownerUserID, toolCallID, status string, exitCode *int, outputExcerpt *string, now time.Time) error {
	if status != ToolCallCompleted && status != ToolCallDenied && status != ToolCallFailed {
		return errors.New("invalid completed tool status")
	}
	if outputExcerpt != nil && len([]byte(*outputExcerpt)) > 8192 {
		return errors.New("tool output excerpt exceeds 8192 bytes")
	}
	fromPredicate := "status = 'started'"
	switch status {
	case ToolCallDenied:
		fromPredicate = "status = 'pending' AND decision = 'deny'"
	case ToolCallFailed:
		// An undecided pending row must use FailPendingAgentToolCall so it
		// contends atomically with a browser decision.
		fromPredicate = "(status = 'started' OR (status = 'pending' AND decision IS NOT NULL))"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tool_calls
		SET status = ?, exit_code = ?, output_excerpt = ?, completed_at = ?
		WHERE id = ? AND `+fromPredicate+`
		AND `+agentToolOwnerPredicate,
		status, exitCode, outputExcerpt, encodeTime(now), toolCallID, ownerUserID)
	if err != nil {
		return fmt.Errorf("finish agent tool call: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		if _, lookupErr := s.AgentToolCallByOwner(ctx, ownerUserID, toolCallID); lookupErr != nil {
			return lookupErr
		}
		return ErrConflict
	}
	return nil
}

func (s *Store) agentMessagesByOwner(ctx context.Context, ownerUserID, sessionID string) ([]AgentMessage, error) {
	return agentMessagesByOwnerQuery(ctx, s.db, ownerUserID, sessionID)
}

func agentMessagesByOwnerQuery(ctx context.Context, queryer agentQueryer, ownerUserID, sessionID string) ([]AgentMessage, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT agent_messages.id, agent_messages.session_id,
		agent_messages.role, agent_messages.content, agent_messages.created_at
		FROM agent_messages JOIN agent_sessions ON agent_sessions.id = agent_messages.session_id
		WHERE agent_messages.session_id = ? AND `+agentSessionOwnerPredicate+`
		ORDER BY agent_messages.created_at, agent_messages.id`, sessionID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list agent messages: %w", err)
	}
	defer rows.Close()
	values := make([]AgentMessage, 0)
	for rows.Next() {
		message, err := scanAgentMessage(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent messages: %w", err)
	}
	return values, nil
}

func (s *Store) agentToolCallsByOwner(ctx context.Context, ownerUserID, sessionID string) ([]ToolCall, error) {
	return agentToolCallsByOwnerQuery(ctx, s.db, ownerUserID, sessionID)
}

func agentToolCallsByOwnerQuery(ctx context.Context, queryer agentQueryer, ownerUserID, sessionID string) ([]ToolCall, error) {
	rows, err := queryer.QueryContext(ctx, toolCallSelect+`
		WHERE tool_calls.session_id = ? AND `+agentToolOwnerPredicate+`
		ORDER BY tool_calls.created_at, tool_calls.id`, sessionID, ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list agent tool calls: %w", err)
	}
	defer rows.Close()
	values := make([]ToolCall, 0)
	for rows.Next() {
		toolCall, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, toolCall)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent tool calls: %w", err)
	}
	return values, nil
}

const agentSessionColumns = `id, user_id, device_id, approval_mode, provider,
	external_session_id, state, created_at, updated_at, archived_at`

const agentSessionSelect = `SELECT ` + agentSessionColumns + ` FROM agent_sessions`

const toolCallSelect = `SELECT tool_calls.id, tool_calls.session_id, tool_calls.name,
	tool_calls.arguments_json, tool_calls.status, tool_calls.decision, tool_calls.exit_code,
	tool_calls.output_excerpt, tool_calls.created_at, tool_calls.completed_at FROM tool_calls`

func agentSessionByOwnerQuery(ctx context.Context, queryer agentQueryer, ownerUserID, sessionID string) (AgentSession, error) {
	return scanAgentSession(queryer.QueryRowContext(ctx, agentSessionSelect+`
		WHERE agent_sessions.id = ? AND `+agentSessionOwnerPredicate, sessionID, ownerUserID))
}

func agentSessionByManagingOwnerQuery(ctx context.Context, queryer agentQueryer, ownerUserID, sessionID string) (AgentSession, error) {
	return scanAgentSession(queryer.QueryRowContext(ctx, agentSessionSelect+`
		WHERE agent_sessions.id = ? AND `+agentSessionOwnershipPredicate, sessionID, ownerUserID))
}

func agentToolCallByOwnerQuery(ctx context.Context, queryer agentQueryer, ownerUserID, toolCallID string) (ToolCall, error) {
	return scanToolCall(queryer.QueryRowContext(ctx, toolCallSelect+`
		WHERE tool_calls.id = ? AND `+agentToolOwnerPredicate, toolCallID, ownerUserID))
}

func scanAgentSession(row rowScanner) (AgentSession, error) {
	var session AgentSession
	var externalSessionID sql.NullString
	var createdAt, updatedAt string
	var archivedAt sql.NullString
	if err := row.Scan(&session.ID, &session.UserID, &session.DeviceID, &session.ApprovalMode,
		&session.Provider, &externalSessionID, &session.State, &createdAt, &updatedAt, &archivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentSession{}, ErrNotFound
		}
		return AgentSession{}, fmt.Errorf("scan agent session: %w", err)
	}
	if externalSessionID.Valid {
		session.ExternalSessionID = &externalSessionID.String
	}
	var err error
	session.CreatedAt, err = decodeTime(createdAt)
	if err != nil {
		return AgentSession{}, err
	}
	session.UpdatedAt, err = decodeTime(updatedAt)
	if err != nil {
		return AgentSession{}, err
	}
	session.ArchivedAt, err = decodeNullableTime(archivedAt)
	if err != nil {
		return AgentSession{}, err
	}
	return session, nil
}

func scanAgentMessage(row rowScanner) (AgentMessage, error) {
	var message AgentMessage
	var createdAt string
	if err := row.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentMessage{}, ErrNotFound
		}
		return AgentMessage{}, fmt.Errorf("scan agent message: %w", err)
	}
	var err error
	message.CreatedAt, err = decodeTime(createdAt)
	if err != nil {
		return AgentMessage{}, err
	}
	return message, nil
}

func scanToolCall(row rowScanner) (ToolCall, error) {
	var toolCall ToolCall
	var decision, outputExcerpt sql.NullString
	var exitCode sql.NullInt64
	var createdAt string
	var completedAt sql.NullString
	if err := row.Scan(&toolCall.ID, &toolCall.SessionID, &toolCall.Name, &toolCall.ArgumentsJSON,
		&toolCall.Status, &decision, &exitCode, &outputExcerpt, &createdAt, &completedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ToolCall{}, ErrNotFound
		}
		return ToolCall{}, fmt.Errorf("scan agent tool call: %w", err)
	}
	if decision.Valid {
		toolCall.Decision = &decision.String
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		toolCall.ExitCode = &value
	}
	if outputExcerpt.Valid {
		toolCall.OutputExcerpt = &outputExcerpt.String
	}
	var err error
	toolCall.CreatedAt, err = decodeTime(createdAt)
	if err != nil {
		return ToolCall{}, err
	}
	toolCall.CompletedAt, err = decodeNullableTime(completedAt)
	if err != nil {
		return ToolCall{}, err
	}
	return toolCall, nil
}

func requireOneRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect rows affected: %w", err)
	}
	if affected != 1 {
		return ErrNotFound
	}
	return nil
}

func validApprovalMode(mode string) bool {
	return mode == AgentApprovalPerCommand || mode == AgentApprovalFullAccess
}

func validAgentSessionState(state string) bool {
	switch state {
	case AgentSessionIdle, AgentSessionRunning, AgentSessionWaitingApproval, AgentSessionFailed:
		return true
	default:
		return false
	}
}
