// PostgreSQL-репозиторий пользователей.
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// UserRepo — репозиторий пользователей поверх *sql.DB (PostgreSQL).
type UserRepo struct {
	db *sql.DB
}

// NewUserRepo собирает репозиторий пользователей.
func NewUserRepo(db *sql.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Create сохраняет нового пользователя. Уникальность email обеспечивает
// UNIQUE-ограничение базы: нарушение переводится в domain.ErrEmailTaken.
func (r *UserRepo) Create(ctx context.Context, u domain.User) (domain.User, error) {
	u.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash, role, created_at)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		u.Email, u.PasswordHash, string(u.Role), u.CreatedAt,
	).Scan(&u.ID)
	if isUniqueViolation(err) {
		return domain.User{}, domain.ErrEmailTaken
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// ByEmail находит пользователя по email; ErrNotFound если нет.
func (r *UserRepo) ByEmail(ctx context.Context, email string) (domain.User, error) {
	return r.scanOne(ctx,
		`SELECT id, email, password_hash, role, email_verified_at, created_at
		 FROM users WHERE email = $1`, email)
}

// ByID находит пользователя по id; ErrNotFound если нет.
func (r *UserRepo) ByID(ctx context.Context, id int64) (domain.User, error) {
	return r.scanOne(ctx,
		`SELECT id, email, password_hash, role, email_verified_at, created_at
		 FROM users WHERE id = $1`, id)
}

func (r *UserRepo) scanOne(ctx context.Context, query string, arg any) (domain.User, error) {
	var u domain.User
	var role string
	var verified sql.NullTime
	err := r.db.QueryRowContext(ctx, query, arg).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &verified, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound{Entity: "user"}
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("select user: %w", err)
	}
	u.Role = domain.Role(role)
	u.EmailVerifiedAt = fromNullTime(verified)
	return u, nil
}

// UpdatePasswordHash меняет хеш пароля; ErrNotFound если пользователя нет.
func (r *UserRepo) UpdatePasswordHash(ctx context.Context, userID int64, hash string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return errIfNoRows(res, "user", userID)
}

// MarkEmailVerified проставляет отметку подтверждения почты.
func (r *UserRepo) MarkEmailVerified(ctx context.Context, userID int64, at time.Time) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE users SET email_verified_at = $1 WHERE id = $2`, at.UTC(), userID)
	if err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	return errIfNoRows(res, "user", userID)
}

// CountAdmins — сколько администраторов в приложении.
func (r *UserRepo) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = $1`, string(domain.RoleAdmin)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}

// errIfNoRows — ErrNotFound, если UPDATE не задел ни одной строки.
func errIfNoRows(res sql.Result, entity string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound{Entity: entity, ID: id}
	}
	return nil
}
