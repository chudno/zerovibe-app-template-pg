// Админские операции над пользователями (встроенная админ-панель).
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// ListAll возвращает всех пользователей (новые сверху). Полный скан — норм для
// админки приложения (сотни-тысячи строк).
func (r *UserRepo) ListAll(ctx context.Context) ([]domain.User, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, email, password_hash, role, email_verified_at, created_at
		 FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("select all users: %w", err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		var u domain.User
		var role string
		var verified, created sql.NullTime
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &role, &verified, &created); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.Role = domain.Role(role)
		u.EmailVerifiedAt = fromNullTime(verified)
		u.CreatedAt = fromNullTime(created)
		users = append(users, u)
	}
	return users, rows.Err()
}

// UpdateRoleAndEmail меняет email/роль/подтверждённость. Всё в одной
// serializable-транзакции: занятость email другим пользователем + текущее
// состояние + запись. Логика «когда ставить отметку подтверждения» — в Go,
// а не в SQL CASE: проще читать и копировать.
func (r *UserRepo) UpdateRoleAndEmail(ctx context.Context, id int64, email string, role domain.Role, verified bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op после Commit

	// Email занят другим пользователем?
	var takenBy int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE email = $1`, email).Scan(&takenBy)
	switch {
	case err == nil && takenBy != id:
		return domain.ErrEmailTaken
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check email: %w", err)
	}

	// Текущее состояние (и существование) пользователя.
	var currentVerified sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT email_verified_at FROM users WHERE id = $1`, id).Scan(&currentVerified)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound{Entity: "user", ID: id}
	}
	if err != nil {
		return fmt.Errorf("select user: %w", err)
	}

	// Отметка подтверждения: снята → NULL; поставлена впервые → сейчас;
	// уже стояла → не трогаем.
	newVerified := sql.NullTime{}
	if verified {
		if currentVerified.Valid {
			newVerified = currentVerified
		} else {
			newVerified = sql.NullTime{Time: time.Now().UTC(), Valid: true}
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET email = $1, role = $2, email_verified_at = $3 WHERE id = $4`,
		email, string(role), newVerified, id); err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return tx.Commit()
}

// DeleteUser удаляет пользователя; последнего администратора удалить нельзя.
// Проверка и удаление — в одной транзакции (иначе два параллельных удаления
// могли бы снести обоих последних админов).
func (r *UserRepo) DeleteUser(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op после Commit

	var role string
	err = tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = $1`, id).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound{Entity: "user", ID: id}
	}
	if err != nil {
		return fmt.Errorf("get user role: %w", err)
	}
	if domain.Role(role) == domain.RoleAdmin {
		var admins int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM users WHERE role = $1`, string(domain.RoleAdmin)).Scan(&admins); err != nil {
			return fmt.Errorf("count admins: %w", err)
		}
		if admins <= 1 {
			return domain.ErrValidation{Field: "role", Msg: "нельзя удалить последнего администратора"}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return tx.Commit()
}
