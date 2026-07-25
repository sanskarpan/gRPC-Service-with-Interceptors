package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const userCapacityLock int64 = 0x677270635f757365

const defaultRetryAttempts = 3
const defaultRetryBackoff = 250 * time.Millisecond
const defaultRetryMaxBackoff = 5 * time.Second

// migrationFiles keeps schema changes versioned with the binary that applies
// them, so every deployment uses the same SQL instead of an ad hoc startup
// string.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// PostgresConfig contains bounded pool, startup, and retry settings for
// PostgreSQL. Zero-valued retry fields fall back to safe runtime defaults in
// NewPostgresRepository.
type PostgresConfig struct {
	URL              string
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	PingTimeout      time.Duration
	RetryAttempts    int
	RetryBackoff     time.Duration
	RetryMaxBackoff  time.Duration
}

// PostgresRepository stores users in PostgreSQL with a versioned bootstrap
// migration and bounded database connection pool.
type PostgresRepository struct {
	db              *sql.DB
	retryAttempts   int
	retryBackoff    time.Duration
	retryMaxBackoff time.Duration
}

// NewPostgresRepository opens, pings, and migrates a PostgreSQL repository.
// Startup retries transient connectivity failures within the caller's context.
// RetryAttempts, RetryBackoff, and RetryMaxBackoff fall back to safe defaults
// (3, 250ms, 5s) when zero-valued.
func NewPostgresRepository(ctx context.Context, cfg PostgresConfig) (*PostgresRepository, error) {
	attempts := cfg.RetryAttempts
	if attempts <= 0 {
		attempts = defaultRetryAttempts
	}
	backoff := cfg.RetryBackoff
	if backoff <= 0 {
		backoff = defaultRetryBackoff
	}
	maxBackoff := cfg.RetryMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = defaultRetryMaxBackoff
	}
	db, err := sql.Open("pgx", cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	repo := &PostgresRepository{db: db, retryAttempts: attempts, retryBackoff: backoff, retryMaxBackoff: maxBackoff}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
		lastErr = repo.migrateAndPing(pingCtx)
		cancel()
		if lastErr == nil {
			return repo, nil
		}
		if attempt < attempts-1 {
			wait := time.Duration(attempt+1) * backoff
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				_ = db.Close()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	_ = db.Close()
	return nil, fmt.Errorf("initialize postgres: %w", lastErr)
}

// retryConfig holds parameters for transient-error backoff applied to every
// CRUD operation.
type retryConfig struct {
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

func (r *PostgresRepository) retryCfg() retryConfig {
	return retryConfig{
		maxAttempts: r.retryAttempts,
		baseBackoff: r.retryBackoff,
		maxBackoff:  r.retryMaxBackoff,
	}
}

// backoffDuration returns an exponential backoff with capped 25 % jitter.
func backoffDuration(base time.Duration, attempt int, cap time.Duration) time.Duration {
	d := base * (1 << attempt)
	if d > cap {
		d = cap
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(half)))
	if err != nil {
		return d
	}
	jitter := time.Duration(n.Int64()) - d/4
	return d + jitter
}

// isRetryableError reports whether err is a transient Postgres / network error
// that may succeed on retry. Application-level errors such as ErrNotFound,
// ErrInvalid, ErrAlreadyExists, and ErrCapacity are never retried. Context
// cancellation is also not retried.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40001", "40P01", "55P03",
			"08000", "08001", "08003", "08004", "08006",
			"53300", "53400",
			"57P01", "57P02", "57P03",
			"58000", "58030", "XX000":
			return true
		}
	}
	return false
}

// retryOperation executes op repeatedly until it succeeds, a non-retryable
// error is returned, the context expires, or the maximum number of attempts
// is exhausted.
func retryOperation(ctx context.Context, cfg retryConfig, op func(context.Context) error) error {
	var lastErr error
	for attempt := range cfg.maxAttempts {
		if attempt > 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			wait := backoffDuration(cfg.baseBackoff, attempt-1, cfg.maxBackoff)
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		lastErr = op(ctx)
		if lastErr == nil {
			return nil
		}
		if !isRetryableError(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (r *PostgresRepository) migrateAndPing(ctx context.Context) error {
	if err := r.db.PingContext(ctx); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() {
		// Rollback after commit returns sql.ErrTxDone and is intentionally ignored.
		_ = tx.Rollback()
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userCapacityLock); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`); err != nil {
		return err
	}

	pending, err := pendingMigrations(ctx, tx)
	if err != nil {
		return err
	}
	// Verify migration chain consistency
	if err := verifyMigrations(ctx, tx); err != nil {
		return err
	}
	for _, m := range pending {
		body, err := migrationFiles.ReadFile("migrations/" + m.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, m.version); err != nil {
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
	}
	return tx.Commit()
}

// migrationRecord pairs a migration filename with the version parsed from its
// numeric prefix.
type migrationRecord struct {
	name    string
	version int64
}

// pendingMigrations returns the embedded migrations that have not yet been
// applied, ordered by ascending version.
func pendingMigrations(ctx context.Context, tx *sql.Tx) ([]migrationRecord, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	all := make([]migrationRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		version, err := parseMigrationVersion(name)
		if err != nil {
			return nil, err
		}
		all = append(all, migrationRecord{name: name, version: version})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].version < all[j].version })

	applied, err := loadAppliedVersions(ctx, tx)
	if err != nil {
		return nil, err
	}
	pending := make([]migrationRecord, 0, len(all))
	for _, m := range all {
		if applied[m.version] {
			continue
		}
		pending = append(pending, m)
	}
	return pending, nil
}

func parseMigrationVersion(name string) (int64, error) {
	base := name
	// Strip direction suffix
	if strings.HasSuffix(base, ".down.sql") {
		base = strings.TrimSuffix(base, ".down.sql")
	} else if strings.HasSuffix(base, ".sql") {
		base = strings.TrimSuffix(base, ".sql")
	} else {
		return 0, fmt.Errorf("migration %q has an unrecognized extension", name)
	}
	idx := strings.Index(base, "_")
	if idx <= 0 {
		return 0, fmt.Errorf("migration %q is missing a numeric version prefix", name)
	}
	version, err := strconv.ParseInt(base[:idx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("migration %q has an invalid version prefix: %w", name, err)
	}
	return version, nil
}

// pendingDownMigrations returns down scripts for all currently applied
// migrations that have a corresponding .down.sql file, ordered in reverse
// (highest version first) so they can be applied safely.
func pendingDownMigrations(ctx context.Context, tx *sql.Tx) ([]migrationRecord, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	downByName := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".down.sql") {
			continue
		}
		version, err := parseMigrationVersion(name)
		if err != nil {
			return nil, err
		}
		downByName[version] = name
	}
	if len(downByName) == 0 {
		return nil, nil
	}
	applied, err := loadAppliedVersions(ctx, tx)
	if err != nil {
		return nil, err
	}
	down := make([]migrationRecord, 0, len(downByName))
	for v := range applied {
		if name, ok := downByName[v]; ok {
			down = append(down, migrationRecord{name: name, version: v})
		}
	}
	sort.Slice(down, func(i, j int) bool { return down[i].version > down[j].version })
	return down, nil
}

// verifyMigrations checks that embedded migration files are sequential and
// that all applied migrations still exist in the embedded set. It returns a
// non-nil error when the migration chain is inconsistent.
func verifyMigrations(ctx context.Context, tx *sql.Tx) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	upVersions := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".down.sql") {
			continue
		}
		v, err := parseMigrationVersion(name)
		if err != nil {
			return err
		}
		upVersions[v] = name
	}
	// Check sequential versions starting from 1
	expected := int64(1)
	for {
		if _, ok := upVersions[expected]; !ok {
			break
		}
		expected++
	}
	if expected == 1 && len(upVersions) > 0 {
		return fmt.Errorf("migration chain does not start at version 1")
	}
	// Check that all applied versions exist in embedded files
	applied, err := loadAppliedVersions(ctx, tx)
	if err != nil {
		return err
	}
	for v := range applied {
		if _, ok := upVersions[v]; !ok {
			return fmt.Errorf("applied migration version %d not found in embedded migrations", v)
		}
	}
	return nil
}

// ApplyDownMigrations rolls back the most recently applied migration. It
// returns the number of down migrations applied. This is an explicit operation
// and is NOT called during normal startup.
func (r *PostgresRepository) ApplyDownMigrations(ctx context.Context, count int) (int, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userCapacityLock); err != nil {
		return 0, err
	}
	down, err := pendingDownMigrations(ctx, tx)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, m := range down {
		if applied >= count {
			break
		}
		body, err := migrationFiles.ReadFile("migrations/" + m.name)
		if err != nil {
			return applied, fmt.Errorf("read down migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return applied, fmt.Errorf("apply down migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = $1`, m.version); err != nil {
			return applied, fmt.Errorf("unrecord migration %d: %w", m.version, err)
		}
		applied++
	}
	if applied == 0 {
		return 0, nil
	}
	return applied, tx.Commit()
}

func loadAppliedVersions(ctx context.Context, tx *sql.Tx) (map[int64]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := make(map[int64]bool)
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return applied, nil
}

// Create inserts a user and serializes capacity checks across writers.
func (r *PostgresRepository) Create(ctx context.Context, user *pb.User, maxUsers int) error {
	if user == nil || user.GetId() == "" || user.GetName() == "" || user.GetEmail() == "" {
		return ErrInvalid
	}
	return retryOperation(ctx, r.retryCfg(), func(ctx context.Context) error {
		createdAt, updatedAt, err := userTimes(user)
		if err != nil {
			return err
		}
		tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return fmt.Errorf("begin create: %w", err)
		}
		defer func() {
			_ = tx.Rollback()
		}()
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userCapacityLock); err != nil {
			return fmt.Errorf("lock user capacity: %w", err)
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
			return fmt.Errorf("count users: %w", err)
		}
		if count >= maxUsers {
			return ErrCapacity
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO users (id, name, email, age, created_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6)`, user.GetId(), user.GetName(), user.GetEmail(), user.GetAge(), createdAt, updatedAt)
		if err != nil {
			if isUniqueViolation(err) {
				return ErrAlreadyExists
			}
			return fmt.Errorf("insert user: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit create: %w", err)
		}
		return nil
	})
}

// Get loads a user and maps SQL absence to ErrNotFound.
func (r *PostgresRepository) Get(ctx context.Context, id string) (user *pb.User, err error) {
	err = retryOperation(ctx, r.retryCfg(), func(ctx context.Context) error {
		user = &pb.User{}
		var createdAt, updatedAt time.Time
		err := r.db.QueryRowContext(ctx, `SELECT id, name, email, age, created_at, updated_at FROM users WHERE id = $1`, id).
			Scan(&user.Id, &user.Name, &user.Email, &user.Age, &createdAt, &updatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			user = nil
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get user: %w", err)
		}
		user.CreatedAt = timestamppb.New(createdAt)
		user.UpdatedAt = timestamppb.New(updatedAt)
		user = proto.Clone(user).(*pb.User)
		return nil
	})
	return
}

// Update replaces a user row atomically with a SELECT FOR UPDATE guard.
func (r *PostgresRepository) Update(ctx context.Context, user *pb.User) error {
	if user == nil || user.GetId() == "" || user.GetName() == "" || user.GetEmail() == "" {
		return ErrInvalid
	}
	return retryOperation(ctx, r.retryCfg(), func(ctx context.Context) error {
		createdAt, updatedAt, err := userTimes(user)
		if err != nil {
			return err
		}
		tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return fmt.Errorf("begin update: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 FOR UPDATE)`, user.GetId()).Scan(&exists); err != nil {
			return fmt.Errorf("lock user for update: %w", err)
		}
		if !exists {
			return ErrNotFound
		}

		result, err := tx.ExecContext(ctx, `
UPDATE users SET name = $1, email = $2, age = $3, created_at = $4, updated_at = $5 WHERE id = $6`,
			user.GetName(), user.GetEmail(), user.GetAge(), createdAt, updatedAt, user.GetId())
		if err != nil {
			return fmt.Errorf("update user: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect update: %w", err)
		}
		if rows == 0 {
			return ErrNotFound
		}
		return tx.Commit()
	})
}

// Delete removes a user and reports missing rows deterministically.
func (r *PostgresRepository) Delete(ctx context.Context, id string) error {
	return retryOperation(ctx, r.retryCfg(), func(ctx context.Context) error {
		result, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect delete: %w", err)
		}
		if rows == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ReadModifyUpdate atomically loads a user, lets the caller mutate a private
// copy under SELECT FOR UPDATE, and stores the result inside one transaction.
// It exists so the service layer does not introduce a lost-update race by
// performing separate Get + Update calls.
// NOTE: mutate is called again on retry. Callers must ensure the function is
// idempotent or that the retry only happens on errors before the mutate call.
func (r *PostgresRepository) ReadModifyUpdate(ctx context.Context, id string, mutate ReadModifyUpdateFn) (user *pb.User, err error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalid
	}
	if mutate == nil {
		return nil, ErrInvalid
	}
	err = retryOperation(ctx, r.retryCfg(), func(ctx context.Context) error {
		tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return fmt.Errorf("begin read-modify-update: %w", err)
		}
		defer func() { _ = tx.Rollback() }()

		loaded := &pb.User{}
		var createdAt, updatedAt time.Time
		row := tx.QueryRowContext(ctx, `SELECT id, name, email, age, created_at, updated_at FROM users WHERE id = $1 FOR UPDATE`, id)
		if err := row.Scan(&loaded.Id, &loaded.Name, &loaded.Email, &loaded.Age, &createdAt, &updatedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock user for read-modify-update: %w", err)
		}
		loaded.CreatedAt = timestamppb.New(createdAt)
		loaded.UpdatedAt = timestamppb.New(updatedAt)

		working := proto.Clone(loaded).(*pb.User)
		if err := mutate(working); err != nil {
			return err
		}
		newCreated, newUpdated, err := userTimes(working)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE users SET name = $1, email = $2, age = $3, created_at = $4, updated_at = $5 WHERE id = $6`,
			working.GetName(), working.GetEmail(), working.GetAge(), newCreated, newUpdated, working.GetId()); err != nil {
			return fmt.Errorf("update user in read-modify-update: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit read-modify-update: %w", err)
		}
		user = proto.Clone(working).(*pb.User)
		return nil
	})
	return
}

// Ping reports current database reachability for readiness checks.
func (r *PostgresRepository) Ping(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	return r.db.PingContext(ctx)
}

// Stats returns a snapshot of the database pool's runtime stats for
// observability. The returned value is a copy safe to log or expose.
func (r *PostgresRepository) Stats() sql.DBStats {
	return r.db.Stats()
}

// Close drains and closes the database pool.
func (r *PostgresRepository) Close() error { return r.db.Close() }

// DB exposes the underlying *sql.DB for advanced use such as registering
// Prometheus pool metrics. Callers must not close the returned value.
func (r *PostgresRepository) DB() *sql.DB { return r.db }

// IsContextError reports whether err originates from a cancelled or expired
// context. It allows callers to distinguish client-side cancellation from
// server-side persistence failures when classifying errors.
func IsContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func userTimes(user *pb.User) (time.Time, time.Time, error) {
	if user.GetCreatedAt() == nil || user.GetUpdatedAt() == nil {
		return time.Time{}, time.Time{}, ErrInvalid
	}
	createdAt := user.GetCreatedAt().AsTime()
	updatedAt := user.GetUpdatedAt().AsTime()
	if !createdAt.IsZero() && !updatedAt.IsZero() {
		return createdAt, updatedAt, nil
	}
	return time.Time{}, time.Time{}, ErrInvalid
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
