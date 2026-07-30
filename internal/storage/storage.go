// Package storage provides durable SQLite delivery persistence.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/German4341374/reliable-webhook-relay/internal/model"
	"github.com/German4341374/reliable-webhook-relay/migrations"
	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound indicates that a delivery does not exist.
	ErrNotFound = errors.New("delivery not found")
	// ErrNotRetryable indicates that a delivered or active record cannot be manually retried.
	ErrNotRetryable = errors.New("delivery is not in dead-letter status")
)

// Store owns the SQLite connection and delivery queries.
type Store struct {
	db *sql.DB
}

// Open creates the database, applies migrations, and recovers interrupted records.
func Open(ctx context.Context, path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func dsn(path string) string {
	pragmas := "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if path == ":memory:" {
		return "file:relay-memory?mode=memory&cache=shared&" + pragmas
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	return "file:" + filepath.ToSlash(absolute) + "?" + pragmas + "&_pragma=journal_mode(WAL)"
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var applied int
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
			entry.Name(),
		).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if applied > 0 {
			continue
		}
		script, err := migrations.Files.ReadFile(entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.ExecContext(ctx, string(script)); err == nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)",
				entry.Name(), formatTime(time.Now().UTC()),
			)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping verifies database availability.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	return nil
}

// CreateDelivery inserts a record, or returns the existing idempotent record.
func (s *Store) CreateDelivery(
	ctx context.Context,
	delivery model.Delivery,
) (model.Delivery, bool, error) {
	var idempotency any
	if delivery.IdempotencyKey != "" {
		idempotency = delivery.IdempotencyKey
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO deliveries (
			id, channel, received_at, status, attempts, next_attempt_at,
			response_status, last_error, payload_hash, payload, content_type,
			idempotency_key, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, NULL, '', ?, ?, ?, ?, ?)`,
		delivery.ID,
		delivery.Channel,
		formatTime(delivery.ReceivedAt),
		delivery.Status,
		delivery.Attempts,
		formatOptionalTime(delivery.NextAttemptAt),
		delivery.PayloadHash,
		delivery.Payload,
		delivery.ContentType,
		idempotency,
		formatTime(time.Now().UTC()),
	)
	if err != nil {
		return model.Delivery{}, false, fmt.Errorf("insert delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.Delivery{}, false, fmt.Errorf("read inserted delivery count: %w", err)
	}
	if affected == 1 {
		return delivery, true, nil
	}
	if delivery.IdempotencyKey == "" {
		return model.Delivery{}, false, errors.New("delivery ID already exists")
	}
	existing, err := s.getByIdempotency(ctx, delivery.Channel, delivery.IdempotencyKey)
	return existing, false, err
}

// GetDelivery returns one delivery without exposing its payload through JSON.
func (s *Store) GetDelivery(ctx context.Context, id string) (model.Delivery, error) {
	row := s.db.QueryRowContext(ctx, selectDelivery+" WHERE id = ?", id)
	delivery, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Delivery{}, ErrNotFound
	}
	if err != nil {
		return model.Delivery{}, fmt.Errorf("get delivery: %w", err)
	}
	return delivery, nil
}

func (s *Store) getByIdempotency(
	ctx context.Context,
	channel, key string,
) (model.Delivery, error) {
	row := s.db.QueryRowContext(ctx,
		selectDelivery+" WHERE channel = ? AND idempotency_key = ?",
		channel, key,
	)
	delivery, err := scanDelivery(row)
	if err != nil {
		return model.Delivery{}, fmt.Errorf("get idempotent delivery: %w", err)
	}
	return delivery, nil
}

// ListDeliveries returns the latest deliveries with optional exact filters.
func (s *Store) ListDeliveries(
	ctx context.Context,
	limit int,
	status, channel string,
) ([]model.Delivery, error) {
	query := selectDelivery + " WHERE 1=1"
	args := make([]any, 0, 3)
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	if channel != "" {
		query += " AND channel = ?"
		args = append(args, channel)
	}
	query += " ORDER BY received_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := make([]model.Delivery, 0)
	for rows.Next() {
		delivery, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("scan delivery list: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delivery list: %w", err)
	}
	return deliveries, nil
}

// ClaimNext atomically moves one due record to processing and increments attempts.
func (s *Store) ClaimNext(ctx context.Context, now time.Time) (*model.Delivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delivery claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, selectDelivery+`
		WHERE status IN ('pending', 'retrying')
		  AND next_attempt_at IS NOT NULL
		  AND next_attempt_at <= ?
		ORDER BY next_attempt_at ASC, received_at ASC
		LIMIT 1`, formatTime(now))
	delivery, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select due delivery: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, attempts = attempts + 1, updated_at = ?
		WHERE id = ? AND status IN ('pending', 'retrying')`,
		model.StatusProcessing, formatTime(now), delivery.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("claim due delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return nil, errors.New("delivery claim lost a concurrent update")
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delivery claim: %w", err)
	}
	delivery.Status = model.StatusProcessing
	delivery.Attempts++
	return &delivery, nil
}

// MarkDelivered records a successful terminal response.
func (s *Store) MarkDelivered(ctx context.Context, id string, responseStatus int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, next_attempt_at = NULL, response_status = ?,
		    last_error = '', updated_at = ?
		WHERE id = ?`,
		model.StatusDelivered, responseStatus, formatTime(time.Now().UTC()), id,
	)
	return wrapUpdateError("mark delivery successful", err)
}

// MarkRetry schedules another attempt.
func (s *Store) MarkRetry(
	ctx context.Context,
	id string,
	next time.Time,
	responseStatus *int,
	lastError string,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, next_attempt_at = ?, response_status = ?,
		    last_error = ?, updated_at = ?
		WHERE id = ?`,
		model.StatusRetrying,
		formatTime(next),
		nullableInt(responseStatus),
		lastError,
		formatTime(time.Now().UTC()),
		id,
	)
	return wrapUpdateError("schedule delivery retry", err)
}

// MarkDeadLetter records a terminal failure.
func (s *Store) MarkDeadLetter(
	ctx context.Context,
	id string,
	responseStatus *int,
	lastError string,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, next_attempt_at = NULL, response_status = ?,
		    last_error = ?, updated_at = ?
		WHERE id = ?`,
		model.StatusDeadLetter,
		nullableInt(responseStatus),
		lastError,
		formatTime(time.Now().UTC()),
		id,
	)
	return wrapUpdateError("mark delivery dead-letter", err)
}

// RetryDeadLetter resets a terminal failure for a fresh attempt cycle.
func (s *Store) RetryDeadLetter(ctx context.Context, id string, now time.Time) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, attempts = 0, next_attempt_at = ?,
		    response_status = NULL, last_error = '', updated_at = ?
		WHERE id = ? AND status = ?`,
		model.StatusRetrying,
		formatTime(now),
		formatTime(now),
		id,
		model.StatusDeadLetter,
	)
	if err != nil {
		return fmt.Errorf("reset dead-letter delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read reset delivery count: %w", err)
	}
	if affected == 1 {
		return nil
	}
	_, getErr := s.GetDelivery(ctx, id)
	if errors.Is(getErr, ErrNotFound) {
		return ErrNotFound
	}
	if getErr != nil {
		return getErr
	}
	return ErrNotRetryable
}

// RecoverInFlight reschedules records left processing by an interrupted process.
func (s *Store) RecoverInFlight(ctx context.Context, now time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		SET status = ?, next_attempt_at = ?, last_error = ?,
		    updated_at = ?
		WHERE status = ?`,
		model.StatusRetrying,
		formatTime(now),
		"relay restarted during delivery",
		formatTime(now),
		model.StatusProcessing,
	)
	if err != nil {
		return 0, fmt.Errorf("recover in-flight deliveries: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read recovered delivery count: %w", err)
	}
	return count, nil
}

const selectDelivery = `
	SELECT id, channel, received_at, status, attempts, next_attempt_at,
	       response_status, last_error, payload_hash, payload, content_type,
	       COALESCE(idempotency_key, '')
	FROM deliveries`

type scanner interface {
	Scan(dest ...any) error
}

func scanDelivery(source scanner) (model.Delivery, error) {
	var delivery model.Delivery
	var receivedAt string
	var nextAttemptAt sql.NullString
	var responseStatus sql.NullInt64
	if err := source.Scan(
		&delivery.ID,
		&delivery.Channel,
		&receivedAt,
		&delivery.Status,
		&delivery.Attempts,
		&nextAttemptAt,
		&responseStatus,
		&delivery.LastError,
		&delivery.PayloadHash,
		&delivery.Payload,
		&delivery.ContentType,
		&delivery.IdempotencyKey,
	); err != nil {
		return model.Delivery{}, err
	}
	parsedReceived, err := time.Parse(time.RFC3339Nano, receivedAt)
	if err != nil {
		return model.Delivery{}, fmt.Errorf("parse received time: %w", err)
	}
	delivery.ReceivedAt = parsedReceived
	if nextAttemptAt.Valid {
		parsedNext, err := time.Parse(time.RFC3339Nano, nextAttemptAt.String)
		if err != nil {
			return model.Delivery{}, fmt.Errorf("parse next attempt time: %w", err)
		}
		delivery.NextAttemptAt = &parsedNext
	}
	if responseStatus.Valid {
		status := int(responseStatus.Int64)
		delivery.ResponseStatus = &status
	}
	return delivery, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func wrapUpdateError(action string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}
