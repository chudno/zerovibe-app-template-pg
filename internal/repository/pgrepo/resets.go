// PostgreSQL-репозиторий токенов сброса пароля.
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// ResetRepo — репозиторий токенов сброса пароля.
type ResetRepo struct {
	db *sql.DB
}

// NewResetRepo собирает репозиторий сбросов.
func NewResetRepo(db *sql.DB) *ResetRepo {
	return &ResetRepo{db: db}
}

// Create сохраняет токен сброса.
func (r *ResetRepo) Create(ctx context.Context, p domain.PasswordReset) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO password_resets (token, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (token) DO NOTHING`,
		p.Token, p.UserID, p.CreatedAt.UTC(), p.ExpiresAt.UTC(),
	); err != nil {
		return fmt.Errorf("insert reset: %w", err)
	}
	return nil
}

// ByToken находит токен сброса; ErrNotFound если нет.
func (r *ResetRepo) ByToken(ctx context.Context, token string) (domain.PasswordReset, error) {
	var p domain.PasswordReset
	var created, expires, used sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT token, user_id, created_at, expires_at, used_at FROM password_resets WHERE token = $1`, token,
	).Scan(&p.Token, &p.UserID, &created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PasswordReset{}, domain.ErrNotFound{Entity: "reset"}
	}
	if err != nil {
		return domain.PasswordReset{}, fmt.Errorf("select reset: %w", err)
	}
	p.CreatedAt = fromNullTime(created)
	p.ExpiresAt = fromNullTime(expires)
	p.UsedAt = fromNullTime(used)
	return p, nil
}

// MarkUsed помечает токен использованным.
func (r *ResetRepo) MarkUsed(ctx context.Context, token string, usedAt time.Time) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE password_resets SET used_at = $1 WHERE token = $2`, usedAt.UTC(), token,
	); err != nil {
		return fmt.Errorf("mark reset used: %w", err)
	}
	return nil
}

// DeleteByUser удаляет токены пользователя (каскад при удалении аккаунта).
func (r *ResetRepo) DeleteByUser(ctx context.Context, userID int64) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM password_resets WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete resets by user: %w", err)
	}
	return nil
}
