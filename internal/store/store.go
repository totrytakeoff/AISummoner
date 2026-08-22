// Package store owns SQLite persistence and transaction boundaries.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aisummoner/aisummoner/migrations"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound       = errors.New("store: not found")
	ErrConflict       = errors.New("store: conflict")
	ErrPairingInvalid = errors.New("store: pairing code invalid")
	ErrPairingExpired = errors.New("store: pairing code expired")
)

// Store serializes the small MVP write workload through one SQLite connection.
type Store struct {
	db *sql.DB
}

type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type WebSession struct {
	ID          string
	UserID      string
	TokenDigest []byte
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type Device struct {
	ID            string
	PublicKey     []byte
	OwnerUserID   *string
	Name          string
	Platform      string
	Arch          string
	ClientVersion string
	CreatedAt     time.Time
	PairedAt      *time.Time
	LastSeenAt    *time.Time
}

type PairingCode struct {
	ID         string
	DeviceID   string
	CodeDigest []byte
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

type AuditEvent struct {
	ID           string
	ActorUserID  *string
	DeviceID     *string
	EventType    string
	MetadataJSON string
	CreatedAt    time.Time
}

// Open creates a private database file, enables required pragmas, and migrates it.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	file, err := os.OpenFile(absolutePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close database file: %w", err)
	}
	if err := os.Chmod(absolutePath, 0o600); err != nil {
		return nil, fmt.Errorf("protect database file: %w", err)
	}

	database, err := sql.Open("sqlite", sqliteDSN(absolutePath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The MVP workload is intentionally serialized. This also makes pairing claim
	// transactions deterministic without relying on driver-specific BEGIN IMMEDIATE.
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	store := &Store{db: database}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := store.migrate(ctx); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) string {
	parsed := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at DATETIME NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", name).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("query migration %s: %w", name, err)
		}
		contents, err := migrations.Files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", name, encodeTime(time.Now())); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// Close releases the SQLite connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func encodeTime(value time.Time) int64 {
	return value.UTC().UnixNano()
}

func decodeTime(value string) (time.Time, error) {
	nanoseconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil {
		return time.Unix(0, nanoseconds).UTC(), nil
	}
	// Accept SQLite driver's RFC3339 representation for compatibility with
	// databases created by early development builds.
	parsed, parseErr := time.Parse(time.RFC3339Nano, value)
	if parseErr != nil {
		return time.Time{}, fmt.Errorf("parse database time: %w", parseErr)
	}
	return parsed.UTC(), nil
}

func decodeNullableTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := decodeTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
