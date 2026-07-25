// PostgreSQL-репозиторий токенов подтверждения почты.
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// EmailVerificationRepo — репозиторий токенов подтверждения почты.
type EmailVerificationRepo struct {
	db *sql.DB
}

// NewEmailVerificationRepo собирает репозиторий подтверждений.
func NewEmailVerificationRepo(db *sql.DB) *EmailVerificationRepo {
	return &EmailVerificationRepo{db: db}
}

// Create сохраняет токен подтверждения.
func (r *EmailVerificationRepo) Create(ctx context.Context, v domain.EmailVerification) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO email_verifications (token, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (token) DO NOTHING`,
		v.Token, v.UserID, v.CreatedAt.UTC(), v.ExpiresAt.UTC(),
	); err != nil {
		return fmt.Errorf("insert email verification: %w", err)
	}
	return nil
}

// ByToken находит токен подтверждения; ErrNotFound если нет.
func (r *EmailVerificationRepo) ByToken(ctx context.Context, token string) (domain.EmailVerification, error) {
	var v domain.EmailVerification
	var created, expires, used sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT token, user_id, created_at, expires_at, used_at FROM email_verifications WHERE token = $1`, token,
	).Scan(&v.Token, &v.UserID, &created, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.EmailVerification{}, domain.ErrNotFound{Entity: "email verification"}
	}
	if err != nil {
		return domain.EmailVerification{}, fmt.Errorf("select email verification: %w", err)
	}
	v.CreatedAt = fromNullTime(created)
	v.ExpiresAt = fromNullTime(expires)
	v.UsedAt = fromNullTime(used)
	return v, nil
}

// MarkUsed помечает токен использованным.
func (r *EmailVerificationRepo) MarkUsed(ctx context.Context, token string, usedAt time.Time) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE email_verifications SET used_at = $1 WHERE token = $2`, usedAt.UTC(), token,
	); err != nil {
		return fmt.Errorf("mark email verification used: %w", err)
	}
	return nil
}

// DeleteByUser удаляет токены пользователя (каскад при удалении аккаунта).
func (r *EmailVerificationRepo) DeleteByUser(ctx context.Context, userID int64) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM email_verifications WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete email verifications by user: %w", err)
	}
	return nil
}
