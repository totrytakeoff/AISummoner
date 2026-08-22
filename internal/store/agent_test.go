package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentStoreLifecycleAndOwnerJoins(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 13, 3, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_owner", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	_, err = database.RegisterDevice(ctx, Device{
		ID: "dev_agent", PublicKey: bytes.Repeat([]byte{0x42}, 32), OwnerUserID: &ownerID,
		Name: "agent host", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := AgentSession{
		ID: "ags_test", UserID: ownerID, DeviceID: "dev_agent", ApprovalMode: AgentApprovalPerCommand,
		Provider: "fake", State: AgentSessionIdle, CreatedAt: now, UpdatedAt: now,
	}
	wrongOwnerSession := session
	wrongOwnerSession.ID = "ags_wrong"
	wrongOwnerSession.UserID = "usr_somebody_else"
	if err := database.CreateAgentSession(ctx, wrongOwnerSession); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner create result = %v", err)
	}
	if err := database.CreateAgentSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AgentSessionByOwner(ctx, "usr_somebody_else", session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner session result = %v", err)
	}
	if err := database.UpdateAgentExternalSessionID(ctx, ownerID, session.ID, "opencode_external", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	message := AgentMessage{ID: "msg_test", SessionID: session.ID, Role: "user", Content: "inspect", CreatedAt: now.Add(2 * time.Second)}
	if err := database.CreateAgentMessage(ctx, ownerID, message); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentMessage(ctx, "usr_somebody_else", AgentMessage{
		ID: "msg_wrong", SessionID: session.ID, Role: "user", Content: "leak", CreatedAt: now,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner message result = %v", err)
	}
	toolCall := ToolCall{
		ID: "tool_test", SessionID: session.ID, Name: "remote_exec",
		ArgumentsJSON: `{"command":"uname -a","timeout_ms":30000}`, Status: ToolCallPending, CreatedAt: now.Add(3 * time.Second),
	}
	if err := database.CreateAgentToolCall(ctx, ownerID, toolCall); err != nil {
		t.Fatal(err)
	}
	autoStarted := ToolCall{
		ID: "tool_auto", SessionID: session.ID, Name: "remote_exec",
		ArgumentsJSON: `{"command":"hostname","timeout_ms":30000}`, Status: ToolCallStarted, CreatedAt: now.Add(3500 * time.Millisecond),
	}
	if err := database.CreateAgentToolCall(ctx, ownerID, autoStarted); err != nil {
		t.Fatal(err)
	}
	storedAuto, err := database.AgentToolCallByOwner(ctx, ownerID, autoStarted.ID)
	if err != nil || storedAuto.Decision != nil {
		t.Fatalf("full-access auto-start fabricated a decision: %#v err=%v", storedAuto, err)
	}
	if _, _, err := database.DecideAgentToolCall(ctx, "usr_somebody_else", toolCall.ID, "approve_once", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner decision result = %v", err)
	}
	decided, upgraded, err := database.DecideAgentToolCall(ctx, ownerID, toolCall.ID, "approve_session", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if decided.Decision == nil || *decided.Decision != "approve_session" || upgraded.ApprovalMode != AgentApprovalFullAccess {
		t.Fatalf("decision/session not persisted: tool=%#v session=%#v", decided, upgraded)
	}
	if _, _, err := database.DecideAgentToolCall(ctx, ownerID, toolCall.ID, "approve_once", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate decision result = %v", err)
	}
	if err := database.StartAgentToolCall(ctx, ownerID, toolCall.ID); err != nil {
		t.Fatal(err)
	}
	exitCode := 7
	excerpt := "remote output"
	if err := database.FinishAgentToolCall(ctx, ownerID, toolCall.ID, ToolCallCompleted, &exitCode, &excerpt, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := database.AgentSnapshotByOwner(ctx, ownerID, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.ExternalSessionID == nil || *snapshot.Session.ExternalSessionID != "opencode_external" ||
		len(snapshot.Messages) != 1 || len(snapshot.ToolCalls) != 2 || snapshot.ToolCalls[0].Status != ToolCallCompleted ||
		snapshot.ToolCalls[0].ExitCode == nil || *snapshot.ToolCalls[0].ExitCode != exitCode {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if _, err := database.AgentSnapshotByOwner(ctx, "usr_somebody_else", session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner snapshot result = %v", err)
	}
}

func TestLatestAgentSnapshotByDeviceOwnerReturnsNewestTranscript(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "agent-latest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_latest", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	deviceID := "dev_latest"
	if _, err := database.RegisterDevice(ctx, Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x71}, 32), OwnerUserID: &ownerID,
		Name: "latest", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for index, sessionID := range []string{"ags_old", "ags_new"} {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		if err := database.CreateAgentSession(ctx, AgentSession{
			ID: sessionID, UserID: ownerID, DeviceID: deviceID, ApprovalMode: AgentApprovalPerCommand,
			Provider: "opencode", State: AgentSessionIdle, CreatedAt: createdAt, UpdatedAt: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.CreateAgentMessage(ctx, ownerID, AgentMessage{
		ID: "msg_reasoning", SessionID: "ags_new", Role: "reasoning", Content: "inspect first", CreatedAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentMessage(ctx, ownerID, AgentMessage{
		ID: "msg_answer", SessionID: "ags_new", Role: "assistant", Content: "done", CreatedAt: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := database.LatestAgentSnapshotByDeviceOwner(ctx, ownerID, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Session.ID != "ags_new" || len(snapshot.Messages) != 2 || snapshot.Messages[0].Role != "reasoning" || snapshot.Messages[1].Content != "done" {
		t.Fatalf("unexpected latest snapshot: %#v", snapshot)
	}
	if _, err := database.LatestAgentSnapshotByDeviceOwner(ctx, "usr_other", deviceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong owner latest snapshot=%v", err)
	}
	if _, err := database.LatestAgentSnapshotByDeviceOwner(ctx, ownerID, "dev_unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown device latest snapshot=%v", err)
	}
}

func TestAgentApprovalAbortAndDecisionHaveOneDurableWinner(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "agent-approval-winner.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 13, 3, 30, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_winner", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	if _, err := database.RegisterDevice(ctx, Device{
		ID: "dev_winner", PublicKey: bytes.Repeat([]byte{0x4a}, 32), OwnerUserID: &ownerID,
		Name: "winner host", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	session := AgentSession{
		ID: "ags_winner", UserID: ownerID, DeviceID: "dev_winner", ApprovalMode: AgentApprovalPerCommand,
		Provider: "fake", State: AgentSessionRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateAgentSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	createPending := func(id string) {
		t.Helper()
		if err := database.CreateAgentToolCall(ctx, ownerID, ToolCall{
			ID: id, SessionID: session.ID, Name: "remote_exec", ArgumentsJSON: `{"command":"hostname"}`,
			Status: ToolCallPending, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	createPending("tool_abort_wins")
	aborted, err := database.FailPendingAgentToolCall(ctx, ownerID, "tool_abort_wins", "approval timeout", now.Add(time.Second))
	if err != nil || aborted.Status != ToolCallFailed || aborted.Decision != nil {
		t.Fatalf("abort winner: tool=%#v err=%v", aborted, err)
	}
	if _, _, err := database.DecideAgentToolCall(ctx, ownerID, "tool_abort_wins", "approve_session", now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("late session approval result=%v", err)
	}
	unchanged, err := database.AgentSessionByOwner(ctx, ownerID, session.ID)
	if err != nil || unchanged.ApprovalMode != AgentApprovalPerCommand {
		t.Fatalf("late approval upgraded session: session=%#v err=%v", unchanged, err)
	}

	createPending("tool_decision_wins")
	decided, upgraded, err := database.DecideAgentToolCall(ctx, ownerID, "tool_decision_wins", "approve_session", now.Add(3*time.Second))
	if err != nil || decided.Decision == nil || *decided.Decision != "approve_session" || upgraded.ApprovalMode != AgentApprovalFullAccess {
		t.Fatalf("decision winner: tool=%#v session=%#v err=%v", decided, upgraded, err)
	}
	loserView, err := database.FailPendingAgentToolCall(ctx, ownerID, "tool_decision_wins", "too late", now.Add(4*time.Second))
	if !errors.Is(err, ErrConflict) || loserView.Status != ToolCallPending || loserView.Decision == nil || *loserView.Decision != "approve_session" {
		t.Fatalf("abort did not reconcile decision winner: tool=%#v err=%v", loserView, err)
	}
}

func TestRevokedSessionOwnerSurfaceAndMutationsStayHiddenAfterRepair(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "agent-revoked.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 13, 7, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_revoked", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	deviceID := "dev_revoked"
	if _, err := database.RegisterDevice(ctx, Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x62}, 32), OwnerUserID: &ownerID,
		Name: "revoked", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	session := AgentSession{
		ID: "ags_revoked", UserID: ownerID, DeviceID: deviceID, ApprovalMode: AgentApprovalFullAccess,
		Provider: "fake", State: AgentSessionRunning, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateAgentSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	pending := ToolCall{ID: "tool_revoked_pending", SessionID: session.ID, Name: "remote_exec", ArgumentsJSON: `{"command":"hostname"}`, Status: ToolCallPending, CreatedAt: now}
	started := ToolCall{ID: "tool_revoked_started", SessionID: session.ID, Name: "remote_exec", ArgumentsJSON: `{"command":"uname"}`, Status: ToolCallStarted, CreatedAt: now}
	if err := database.CreateAgentToolCall(ctx, ownerID, pending); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentToolCall(ctx, ownerID, started); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UnpairDevice(ctx, ownerID, deviceID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Simulate a same-owner re-pair without relying on Pairing service details.
	if _, err := database.db.ExecContext(ctx,
		"UPDATE devices SET owner_user_id = ?, paired_at = ? WHERE id = ?", ownerID, encodeTime(now.Add(2*time.Minute)), deviceID); err != nil {
		t.Fatal(err)
	}

	assertNotFound := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s result = %v", name, err)
		}
	}
	_, err = database.AgentSessionByOwner(ctx, ownerID, session.ID)
	assertNotFound("session", err)
	_, err = database.AgentSnapshotByOwner(ctx, ownerID, session.ID)
	assertNotFound("snapshot", err)
	_, err = database.AgentToolCallByOwner(ctx, ownerID, pending.ID)
	assertNotFound("tool lookup", err)
	assertNotFound("state update", database.UpdateAgentSessionState(ctx, ownerID, session.ID, AgentSessionIdle, now))
	assertNotFound("begin", database.BeginAgentTurn(ctx, ownerID, session.ID, now))
	assertNotFound("external ID", database.UpdateAgentExternalSessionID(ctx, ownerID, session.ID, "ses_new", now))
	assertNotFound("message", database.CreateAgentMessage(ctx, ownerID, AgentMessage{ID: "msg_after", SessionID: session.ID, Role: "user", Content: "no", CreatedAt: now}))
	assertNotFound("tool create", database.CreateAgentToolCall(ctx, ownerID, ToolCall{ID: "tool_after", SessionID: session.ID, Name: "remote_exec", ArgumentsJSON: `{}`, Status: ToolCallStarted, CreatedAt: now}))
	_, _, err = database.DecideAgentToolCall(ctx, ownerID, pending.ID, "approve_session", now)
	assertNotFound("decision", err)
	_, err = database.FailPendingAgentToolCall(ctx, ownerID, pending.ID, "no", now)
	assertNotFound("pending failure", err)
	assertNotFound("start", database.StartAgentToolCall(ctx, ownerID, pending.ID))
	excerpt := "no"
	assertNotFound("finish", database.FinishAgentToolCall(ctx, ownerID, started.ID, ToolCallCompleted, nil, &excerpt, now))

	var state, approval string
	if err := database.db.QueryRowContext(ctx, "SELECT state, approval_mode FROM agent_sessions WHERE id = ?", session.ID).Scan(&state, &approval); err != nil {
		t.Fatal(err)
	}
	if state != AgentSessionRevoked || approval != AgentApprovalFullAccess {
		t.Fatalf("revoked Session changed: state=%q approval=%q", state, approval)
	}
}
