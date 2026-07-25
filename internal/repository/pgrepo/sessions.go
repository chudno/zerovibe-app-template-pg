// PostgreSQL-репозиторий сессий.
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// SessionRepo — репозиторий сессий.
type SessionRepo struct {
	db *sql.DB
}

// NewSessionRepo собирает репозиторий сессий.
func NewSessionRepo(db *sql.DB) *SessionRepo {
	return &SessionRepo{db: db}
}

// Create сохраняет сессию.
func (r *SessionRepo) Create(ctx context.Context, sess domain.Session) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO sessions (token, user_id, created_at, expires_at) VALUES ($1, $2, $3, $4)
		 ON CONFLICT (token) DO NOTHING`,
		sess.Token, sess.UserID, sess.CreatedAt.UTC(), sess.ExpiresAt.UTC(),
	); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// ByToken находит сессию по токену; ErrNotFound если нет.
func (r *SessionRepo) ByToken(ctx context.Context, token string) (domain.Session, error) {
	var sess domain.Session
	var created, expires sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT token, user_id, created_at, expires_at FROM sessions WHERE token = $1`, token,
	).Scan(&sess.Token, &sess.UserID, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, domain.ErrNotFound{Entity: "session"}
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("select session: %w", err)
	}
	sess.CreatedAt = fromNullTime(created)
	sess.ExpiresAt = fromNullTime(expires)
	return sess, nil
}

// Delete удаляет сессию (logout). Отсутствие строки — не ошибка.
func (r *SessionRepo) Delete(ctx context.Context, token string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE token = $1`, token); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteByUser удаляет все сессии пользователя (смена пароля, удаление аккаунта).
// DELETE по не-ключевой колонке — индекс idx_sessions_user в миграции.
func (r *SessionRepo) DeleteByUser(ctx context.Context, userID int64) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete sessions by user: %w", err)
	}
	return nil
}

// DeleteExpired удаляет истёкшие сессии (фоновый GC).
func (r *SessionRepo) DeleteExpired(ctx context.Context, now time.Time) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < $1`, now.UTC()); err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	return nil
}
