// PostgreSQL-репозиторий счётчиков рейт-лимита (fixed-window).
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RateLimitRepo — репозиторий счётчиков попыток.
type RateLimitRepo struct {
	db *sql.DB
}

// NewRateLimitRepo собирает репозиторий рейт-лимита.
func NewRateLimitRepo(db *sql.DB) *RateLimitRepo {
	return &RateLimitRepo{db: db}
}

// Allow учитывает попытку по ключу и сообщает, не превышен ли лимит.
// Fixed-window: окно истекло — начинаем новое. Чтение и запись счётчика — в
// одной serializable-транзакции, чтобы параллельные попытки не теряли инкременты.
func (r *RateLimitRepo) Allow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (bool, time.Duration, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op после Commit

	var start sql.NullTime
	var count int64
	err = tx.QueryRowContext(ctx,
		`SELECT window_start, count FROM rate_counters WHERE key = $1`, key,
	).Scan(&start, &count)

	windowStart := now.UTC()
	switch {
	case errors.Is(err, sql.ErrNoRows):
		count = 0 // первой попытки ещё не было — заводим окно
	case err != nil:
		return false, 0, fmt.Errorf("select rate counter: %w", err)
	default:
		windowStart = fromNullTime(start)
		if now.Sub(windowStart) >= window {
			count = 0 // окно истекло — новое окно
			windowStart = now.UTC()
		}
	}

	count++
	allowed := count <= int64(limit)
	var retryAfter time.Duration
	if !allowed {
		retryAfter = window - now.Sub(windowStart)
		if retryAfter < 0 {
			retryAfter = 0
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rate_counters (key, window_start, count) VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET window_start = EXCLUDED.window_start, count = EXCLUDED.count`,
		key, windowStart, count,
	); err != nil {
		return false, 0, fmt.Errorf("upsert rate counter: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("commit: %w", err)
	}
	return allowed, retryAfter, nil
}
