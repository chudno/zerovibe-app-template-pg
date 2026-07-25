// PostgreSQL-репозиторий настроек приложения (key-value).
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/chudno/zerovibe/internal/domain"
)

// SettingRepo — репозиторий настроек.
type SettingRepo struct {
	db *sql.DB
}

// NewSettingRepo собирает репозиторий настроек.
func NewSettingRepo(db *sql.DB) *SettingRepo {
	return &SettingRepo{db: db}
}

// Get возвращает настройку по ключу; ErrNotFound если нет.
func (r *SettingRepo) Get(ctx context.Context, key string) (domain.Setting, error) {
	var st domain.Setting
	var updated sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT key, value, updated_at FROM settings WHERE key = $1`, key,
	).Scan(&st.Key, &st.Value, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Setting{}, domain.ErrNotFound{Entity: "setting"}
	}
	if err != nil {
		return domain.Setting{}, fmt.Errorf("select setting: %w", err)
	}
	st.UpdatedAt = fromNullTime(updated)
	return st, nil
}

// Set сохраняет настройку (создаёт или перезаписывает) — UPSERT:
// одна атомарная операция, отдельные INSERT/UPDATE не нужны.
func (r *SettingRepo) Set(ctx context.Context, st domain.Setting) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		st.Key, st.Value, st.UpdatedAt.UTC(),
	); err != nil {
		return fmt.Errorf("upsert setting: %w", err)
	}
	return nil
}

// List возвращает все настройки.
func (r *SettingRepo) List(ctx context.Context) ([]domain.Setting, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value, updated_at FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("select settings: %w", err)
	}
	defer rows.Close()
	var out []domain.Setting
	for rows.Next() {
		var st domain.Setting
		var updated sql.NullTime
		if err := rows.Scan(&st.Key, &st.Value, &updated); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		st.UpdatedAt = fromNullTime(updated)
		out = append(out, st)
	}
	return out, rows.Err()
}
