// PostgreSQL-репозиторий счётчиков рейт-лимита (fixed-window).
package pgrepo

import (
	"context"
	"database/sql"
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

// allowSQL учитывает попытку одним атомарным запросом: строку ключа блокирует сам
// INSERT … ON CONFLICT, поэтому параллельные попытки не теряют инкременты.
// $1 — ключ, $2 — now, $3 — now-window: окно истекло, если window_start <= $3
// (то же, что now.Sub(window_start) >= window).
const allowSQL = `
INSERT INTO rate_counters (key, window_start, count) VALUES ($1, $2, 1)
ON CONFLICT (key) DO UPDATE SET
    count = CASE WHEN rate_counters.window_start <= $3 THEN 1 ELSE rate_counters.count + 1 END,
    window_start = CASE WHEN rate_counters.window_start <= $3 THEN $2 ELSE rate_counters.window_start END
RETURNING count, window_start`

// Allow учитывает попытку по ключу и сообщает, не превышен ли лимит.
// Fixed-window: окно истекло — начинаем новое. Счётчик увеличивается одним запросом
// (см. allowSQL), allowed и retryAfter считаются по возвращённым значениям.
func (r *RateLimitRepo) Allow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (bool, time.Duration, error) {
	var count int64
	var start time.Time
	if err := r.db.QueryRowContext(ctx, allowSQL, key, now.UTC(), now.Add(-window).UTC()).Scan(&count, &start); err != nil {
		return false, 0, fmt.Errorf("upsert rate counter: %w", err)
	}

	allowed := count <= int64(limit)
	var retryAfter time.Duration
	if !allowed {
		retryAfter = window - now.Sub(start)
		if retryAfter < 0 {
			retryAfter = 0
		}
	}
	return allowed, retryAfter, nil
}

// Reset удаляет счётчик ключа (например, после успешного входа). Нет счётчика — не ошибка.
func (r *RateLimitRepo) Reset(ctx context.Context, key string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM rate_counters WHERE key = $1`, key); err != nil {
		return fmt.Errorf("delete rate counter: %w", err)
	}
	return nil
}
