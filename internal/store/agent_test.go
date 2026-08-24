package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

func TestRecentAgentSessionsByDeviceOwnerIsBoundedOrderedAndOwnerScoped(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "agent-index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_index", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	deviceID := "dev_index"
	if _, err := database.RegisterDevice(ctx, Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x79}, 32), OwnerUserID: &ownerID,
		Name: "indexed", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 53; index++ {
		createdAt := now.Add(time.Duration(index) * time.Minute)
		sessionID := fmt.Sprintf("ags_%03d", index)
		if err := database.CreateAgentSession(ctx, AgentSession{
			ID: sessionID, UserID: ownerID, DeviceID: deviceID, ApprovalMode: AgentApprovalPerCommand,
			Provider: "deepseek", State: AgentSessionIdle, CreatedAt: createdAt, UpdatedAt: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	longTitle := "  inspect\n\t" + strings.Repeat("x", 100) + "  "
	if err := database.CreateAgentMessage(ctx, ownerID, AgentMessage{
		ID: "msg_index_title", SessionID: "ags_052", Role: "user", Content: longTitle, CreatedAt: now.Add(54 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentMessage(ctx, ownerID, AgentMessage{
		ID: "msg_index_later", SessionID: "ags_052", Role: "user", Content: "must not replace the title", CreatedAt: now.Add(55 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, "UPDATE agent_sessions SET state = ? WHERE id = ?", AgentSessionRevoked, "ags_051"); err != nil {
		t.Fatal(err)
	}

	values, err := database.RecentAgentSessionsByDeviceOwner(ctx, ownerID, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != recentAgentSessionLimit {
		t.Fatalf("session index size=%d want=%d", len(values), recentAgentSessionLimit)
	}
	if values[0].ID != "ags_052" || values[1].ID != "ags_050" || values[len(values)-1].ID != "ags_002" {
		t.Fatalf("unexpected bounded order: first=%q second=%q last=%q", values[0].ID, values[1].ID, values[len(values)-1].ID)
	}
	if values[0].Title != "inspect "+strings.Repeat("x", 71)+"…" || len([]rune(values[0].Title)) != agentSessionTitleRunes {
		t.Fatalf("unexpected normalized title %q (%d runes)", values[0].Title, len([]rune(values[0].Title)))
	}
	if values[1].Title != "New conversation" {
		t.Fatalf("empty title fallback=%q", values[1].Title)
	}
	for _, value := range values {
		if value.ID == "ags_051" {
			t.Fatal("revoked Session appeared in recent index")
		}
	}

	for name, request := range map[string]struct{ owner, device string }{
		"wrong owner":    {owner: "usr_other", device: deviceID},
		"unknown device": {owner: ownerID, device: "dev_unknown"},
	} {
		hidden, err := database.RecentAgentSessionsByDeviceOwner(ctx, request.owner, request.device)
		if err != nil || len(hidden) != 0 {
			t.Fatalf("%s index=%#v err=%v", name, hidden, err)
		}
	}
	if _, err := database.UnpairDevice(ctx, ownerID, deviceID, now.Add(60*time.Minute)); err != nil {
		t.Fatal(err)
	}
	hidden, err := database.RecentAgentSessionsByDeviceOwner(ctx, ownerID, deviceID)
	if err != nil || len(hidden) != 0 {
		t.Fatalf("former owner index=%#v err=%v", hidden, err)
	}
	repairDigest := bytes.Repeat([]byte{0x7a}, 32)
	if err := database.CreatePairingCode(ctx, PairingCode{
		ID: "pair_index_repair", DeviceID: deviceID, CodeDigest: repairDigest,
		CreatedAt: now.Add(61 * time.Minute), ExpiresAt: now.Add(71 * time.Minute),
	}, now.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ClaimPairing(ctx, ownerID, repairDigest, now.Add(62*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentSession(ctx, AgentSession{
		ID: "ags_fresh", UserID: ownerID, DeviceID: deviceID, ApprovalMode: AgentApprovalPerCommand,
		Provider: "deepseek", State: AgentSessionIdle, CreatedAt: now.Add(63 * time.Minute), UpdatedAt: now.Add(63 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	visible, err := database.RecentAgentSessionsByDeviceOwner(ctx, ownerID, deviceID)
	if err != nil || len(visible) != 1 || visible[0].ID != "ags_fresh" {
		t.Fatalf("same-owner re-pair revived old Sessions: %#v err=%v", visible, err)
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

func TestAgentSettingsPermissionArchiveRestoreAndDeleteAreOwnerScoped(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "agent-management.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_management", "admin", "test-phc", now)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := owner.ID
	deviceID := "dev_management"
	if _, err := database.RegisterDevice(ctx, Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x83}, 32), OwnerUserID: &ownerID,
		Name: "managed device", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	settings, err := database.AgentSettingsByOwner(ctx, ownerID)
	if err != nil || settings.DefaultApprovalMode != AgentApprovalPerCommand || settings.UpdatedAt != nil {
		t.Fatalf("default settings=%#v err=%v", settings, err)
	}
	settings, err = database.UpdateAgentSettings(ctx, ownerID, AgentApprovalFullAccess, now.Add(time.Second))
	if err != nil || settings.DefaultApprovalMode != AgentApprovalFullAccess || settings.UpdatedAt == nil {
		t.Fatalf("updated settings=%#v err=%v", settings, err)
	}
	if _, err := database.UpdateAgentSettings(ctx, "usr_other", AgentApprovalFullAccess, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-owner settings update=%v", err)
	}

	session := AgentSession{
		ID: "ags_management", UserID: ownerID, DeviceID: deviceID, ApprovalMode: AgentApprovalPerCommand,
		Provider: "dsh", State: AgentSessionIdle, CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateAgentSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentMessage(ctx, ownerID, AgentMessage{
		ID: "msg_management", SessionID: session.ID, Role: "user", Content: "keep this transcript", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateAgentToolCall(ctx, ownerID, ToolCall{
		ID: "tool_management", SessionID: session.ID, Name: "remote_exec", ArgumentsJSON: `{"command":"hostname"}`,
		Status: ToolCallStarted, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := database.UpdateAgentSessionApprovalMode(ctx, ownerID, session.ID, AgentApprovalFullAccess, now.Add(2*time.Second))
	if err != nil || updated.ApprovalMode != AgentApprovalFullAccess {
		t.Fatalf("permission update=%#v err=%v", updated, err)
	}
	if _, err := database.db.ExecContext(ctx, "UPDATE agent_sessions SET state = ? WHERE id = ?", AgentSessionRunning, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.UpdateAgentSessionApprovalMode(ctx, ownerID, session.ID, AgentApprovalPerCommand, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("running permission update=%v", err)
	}
	if _, err := database.SetAgentSessionArchived(ctx, ownerID, session.ID, true, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("running archive=%v", err)
	}
	if _, err := database.db.ExecContext(ctx, "UPDATE agent_sessions SET state = ? WHERE id = ?", AgentSessionFailed, session.ID); err != nil {
		t.Fatal(err)
	}

	archived, err := database.SetAgentSessionArchived(ctx, ownerID, session.ID, true, now.Add(3*time.Second))
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive=%#v err=%v", archived, err)
	}
	if _, err := database.AgentSessionByOwner(ctx, ownerID, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived session remained active: %v", err)
	}
	active, err := database.RecentAgentSessionsByDeviceOwner(ctx, ownerID, deviceID)
	if err != nil || len(active) != 0 {
		t.Fatalf("active index after archive=%#v err=%v", active, err)
	}
	archivedIndex, err := database.ArchivedAgentSessionsByOwner(ctx, ownerID)
	if err != nil || len(archivedIndex) != 1 || archivedIndex[0].ID != session.ID ||
		archivedIndex[0].DeviceName != "managed device" || archivedIndex[0].Title != "keep this transcript" {
		t.Fatalf("archived index=%#v err=%v", archivedIndex, err)
	}
	wrongOwnerIndex, err := database.ArchivedAgentSessionsByOwner(ctx, "usr_other")
	if err != nil || len(wrongOwnerIndex) != 0 {
		t.Fatalf("wrong-owner archived index=%#v err=%v", wrongOwnerIndex, err)
	}
	restored, err := database.SetAgentSessionArchived(ctx, ownerID, session.ID, false, now.Add(4*time.Second))
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore=%#v err=%v", restored, err)
	}
	if _, err := database.AgentSessionByOwner(ctx, ownerID, session.ID); err != nil {
		t.Fatalf("restored session unavailable: %v", err)
	}

	deleted, err := database.DeleteAgentSessionByOwner(ctx, ownerID, session.ID)
	if err != nil || deleted.ID != session.ID {
		t.Fatalf("delete=%#v err=%v", deleted, err)
	}
	for table, id := range map[string]string{
		"agent_sessions": session.ID, "agent_messages": "msg_management", "tool_calls": "tool_management",
	} {
		var count int
		if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s cascade count=%d err=%v", table, count, err)
		}
	}
}
