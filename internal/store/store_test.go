package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionExpiryAndPairingTransaction(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	admin, created, err := database.BootstrapAdmin(ctx, "usr_admin", "admin", "test-phc", now)
	if err != nil || !created {
		t.Fatalf("BootstrapAdmin: user=%#v created=%v err=%v", admin, created, err)
	}
	other, created, err := database.BootstrapAdmin(ctx, "usr_other", "other", "different", now.Add(time.Second))
	if err != nil || created || other.ID != admin.ID {
		t.Fatalf("second bootstrap changed admin: user=%#v created=%v err=%v", other, created, err)
	}

	digest := bytes.Repeat([]byte{0x11}, 32)
	if err := database.CreateWebSession(ctx, WebSession{
		ID: "ses_test", UserID: admin.ID, TokenDigest: digest,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateWebSession: %v", err)
	}
	if _, err := database.UserBySessionDigest(ctx, digest, now.Add(59*time.Minute)); err != nil {
		t.Fatalf("valid session rejected: %v", err)
	}
	if _, err := database.UserBySessionDigest(ctx, digest, now.Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session result: %v", err)
	}

	registered, err := database.RegisterDevice(ctx, Device{
		ID: "dev_test", PublicKey: bytes.Repeat([]byte{0x22}, 32), Name: "test host",
		Platform: "linux", Arch: "amd64", ClientVersion: "0.1.0", CreatedAt: now,
	})
	if err != nil || registered.OwnerUserID != nil {
		t.Fatalf("RegisterDevice: device=%#v err=%v", registered, err)
	}
	codeDigest := bytes.Repeat([]byte{0x33}, 32)
	if err := database.CreatePairingCode(ctx, PairingCode{
		ID: "pair_test", DeviceID: registered.ID, CodeDigest: codeDigest,
		CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute),
	}, now); err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	claimed, err := database.ClaimPairing(ctx, admin.ID, codeDigest, now.Add(time.Minute))
	if err != nil || claimed.OwnerUserID == nil || *claimed.OwnerUserID != admin.ID {
		t.Fatalf("ClaimPairing: device=%#v err=%v", claimed, err)
	}
	if _, err := database.ClaimPairing(ctx, admin.ID, codeDigest, now.Add(2*time.Minute)); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("consumed pairing code result: %v", err)
	}
	if _, err := database.DeviceByOwner(ctx, "usr_not_owner", registered.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner lookup result: %v", err)
	}
	if _, err := database.UnpairDevice(ctx, "usr_not_owner", registered.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner unpair result: %v", err)
	}
}

func TestPairingExpiry(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	admin, _, err := database.BootstrapAdmin(ctx, "usr_admin", "admin", "test-phc", now)
	if err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}
	_, err = database.RegisterDevice(ctx, Device{
		ID: "dev_expired", PublicKey: bytes.Repeat([]byte{0x44}, 32), Name: "expired host",
		Platform: "linux", Arch: "arm64", ClientVersion: "0.1.0", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	digest := bytes.Repeat([]byte{0x55}, 32)
	if err := database.CreatePairingCode(ctx, PairingCode{
		ID: "pair_expired", DeviceID: "dev_expired", CodeDigest: digest,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}, now); err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	if _, err := database.ClaimPairing(ctx, admin.ID, digest, now.Add(time.Minute)); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired pairing claim result: %v", err)
	}
}

func TestRequiredSQLitePragmas(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer database.Close()
	var journalMode string
	var foreignKeys, busyTimeout int
	if err := database.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if err := database.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if err := database.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("busy_timeout: %v", err)
	}
	if journalMode != "wal" || foreignKeys != 1 || busyTimeout != 5000 {
		t.Fatalf("unexpected SQLite pragmas: journal=%q foreign_keys=%d busy_timeout=%d", journalMode, foreignKeys, busyTimeout)
	}
}

func TestUnpairAtomicallyRevokesSessionsToolsAndCodes(t *testing.T) {
	ctx := context.Background()
	database, ownerID, deviceID, now := unpairStoreFixture(t)
	defer database.Close()
	createUnpairSession(t, database, ownerID, deviceID, "ags_z", AgentApprovalFullAccess, AgentSessionRunning, now)
	createUnpairSession(t, database, ownerID, deviceID, "ags_a", AgentApprovalPerCommand, AgentSessionWaitingApproval, now)
	createUnpairTool(t, database, ownerID, "ags_z", "tool_started", ToolCallStarted, now)
	createUnpairTool(t, database, ownerID, "ags_a", "tool_pending", ToolCallPending, now)

	result, err := database.UnpairDevice(ctx, ownerID, deviceID, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got := result.RevokedAgentSessionIDs; len(got) != 2 || got[0] != "ags_a" || got[1] != "ags_z" {
		t.Fatalf("revoked IDs = %#v", got)
	}
	assertUnpairState(t, database, ownerID, deviceID, true)

	// Re-pairing the same Device to the same owner must not revive the old
	// full-access or idle product Sessions.
	repairDigest := bytes.Repeat([]byte{0x72}, 32)
	if err := database.CreatePairingCode(ctx, PairingCode{
		ID: "pair_repair", DeviceID: deviceID, CodeDigest: repairDigest,
		CreatedAt: now.Add(2 * time.Minute), ExpiresAt: now.Add(12 * time.Minute),
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ClaimPairing(ctx, ownerID, repairDigest, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range result.RevokedAgentSessionIDs {
		if _, err := database.AgentSessionByOwner(ctx, ownerID, sessionID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("re-pair revived %s: %v", sessionID, err)
		}
	}
}

func TestUnpairRollbackRestoresAllFourEffects(t *testing.T) {
	for _, target := range []string{"tool_calls", "devices"} {
		t.Run(target, func(t *testing.T) {
			ctx := context.Background()
			database, ownerID, deviceID, now := unpairStoreFixture(t)
			defer database.Close()
			createUnpairSession(t, database, ownerID, deviceID, "ags_rollback", AgentApprovalFullAccess, AgentSessionRunning, now)
			createUnpairTool(t, database, ownerID, "ags_rollback", "tool_rollback", ToolCallStarted, now)
			trigger := "fail_unpair_" + target
			statement := `CREATE TRIGGER ` + trigger + ` BEFORE UPDATE ON ` + target + `
				BEGIN SELECT RAISE(ABORT, 'injected unpair failure'); END`
			if _, err := database.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
			if _, err := database.UnpairDevice(ctx, ownerID, deviceID, now.Add(time.Minute)); err == nil {
				t.Fatal("fault-injected unpair succeeded")
			}
			if _, err := database.db.ExecContext(ctx, "DROP TRIGGER "+trigger); err != nil {
				t.Fatal(err)
			}
			assertUnpairState(t, database, ownerID, deviceID, false)
		})
	}
}

func unpairStoreFixture(t *testing.T) (*Store, string, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "unpair.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 13, 6, 0, 0, 0, time.UTC)
	owner, _, err := database.BootstrapAdmin(ctx, "usr_unpair", "admin", "test-phc", now)
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	deviceID := "dev_unpair"
	if _, err := database.RegisterDevice(ctx, Device{
		ID: deviceID, PublicKey: bytes.Repeat([]byte{0x71}, 32), OwnerUserID: &owner.ID,
		Name: "unpair", Platform: "linux", Arch: "amd64", ClientVersion: "test", CreatedAt: now,
	}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO pairing_codes
		(id, device_id, code_digest, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, NULL, ?)`, "pair_active", deviceID, bytes.Repeat([]byte{0x73}, 32),
		encodeTime(now.Add(10*time.Minute)), encodeTime(now)); err != nil {
		database.Close()
		t.Fatal(err)
	}
	return database, owner.ID, deviceID, now
}

func createUnpairSession(t *testing.T, database *Store, ownerID, deviceID, sessionID, approval, state string, now time.Time) {
	t.Helper()
	if err := database.CreateAgentSession(context.Background(), AgentSession{
		ID: sessionID, UserID: ownerID, DeviceID: deviceID, ApprovalMode: approval,
		Provider: "fake", State: state, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func createUnpairTool(t *testing.T, database *Store, ownerID, sessionID, toolID, status string, now time.Time) {
	t.Helper()
	if err := database.CreateAgentToolCall(context.Background(), ownerID, ToolCall{
		ID: toolID, SessionID: sessionID, Name: "remote_exec", ArgumentsJSON: `{"command":"sentinel"}`,
		Status: status, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertUnpairState(t *testing.T, database *Store, ownerID, deviceID string, unpaired bool) {
	t.Helper()
	ctx := context.Background()
	device, err := database.DeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if unpaired != (device.OwnerUserID == nil) {
		t.Fatalf("owner state after unpair=%v: %#v", unpaired, device.OwnerUserID)
	}
	var activeCodes int
	if err := database.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pairing_codes WHERE device_id = ? AND consumed_at IS NULL", deviceID).Scan(&activeCodes); err != nil {
		t.Fatal(err)
	}
	var sessionState, toolStatus string
	var excerpt sql.NullString
	if err := database.db.QueryRowContext(ctx, "SELECT state FROM agent_sessions WHERE id = 'ags_rollback'").Scan(&sessionState); err != nil {
		// The success fixture uses different IDs.
		if err := database.db.QueryRowContext(ctx, "SELECT state FROM agent_sessions ORDER BY id LIMIT 1").Scan(&sessionState); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.db.QueryRowContext(ctx,
		"SELECT status, output_excerpt FROM tool_calls ORDER BY id LIMIT 1").Scan(&toolStatus, &excerpt); err != nil {
		t.Fatal(err)
	}
	if unpaired {
		if activeCodes != 0 || sessionState != AgentSessionRevoked || toolStatus != ToolCallFailed || !excerpt.Valid || excerpt.String != deviceUnpairedToolExcerpt {
			t.Fatalf("partial unpair: codes=%d session=%q tool=%q excerpt=%v", activeCodes, sessionState, toolStatus, excerpt)
		}
		if _, err := database.DeviceByOwner(ctx, ownerID, deviceID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unpaired Device owner lookup = %v", err)
		}
	} else if activeCodes != 1 || sessionState == AgentSessionRevoked || toolStatus == ToolCallFailed || excerpt.Valid {
		t.Fatalf("rollback leaked mutation: codes=%d session=%q tool=%q excerpt=%v", activeCodes, sessionState, toolStatus, excerpt)
	}
}
