// Package pgrepo — реализация портов usecase поверх PostgreSQL (database/sql).
//
// ОБРАЗЕЦ ДЛЯ ГЕНЕРАЦИИ: на каждую сущность — свой репозиторий, реализующий
// порт из usecase. Это обычный PostgreSQL (полный свод — в package doc
// internal/repository/pgrepo):
//   - новая строка: `INSERT ... RETURNING id` (id выдаёт база);
//   - время: time.Time напрямую в timestamptz-колонки, NULL = «не было»;
//   - плейсхолдеры $1, $2, …; UNIQUE и внешние ключи — в схеме;
//   - «была ли строка» после UPDATE/DELETE — через RowsAffected.
package pgrepo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// NoteRepo — PostgreSQL-репозиторий заметок.
type NoteRepo struct {
	db *sql.DB
}

// NewNoteRepo собирает репозиторий заметок.
func NewNoteRepo(db *sql.DB) *NoteRepo {
	return &NoteRepo{db: db}
}

// Create сохраняет заметку владельца и возвращает её с проставленными id и
// created_at — фрагмент сразу показывает время.
func (r *NoteRepo) Create(ctx context.Context, n domain.Note) (domain.Note, error) {
	n.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO notes (owner_id, title, body, due_date, created_at)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		n.OwnerID, n.Title, n.Body, toNullString(n.DueDate), n.CreatedAt,
	).Scan(&n.ID)
	if err != nil {
		return domain.Note{}, fmt.Errorf("insert note: %w", err)
	}
	return n, nil
}

// ListByOwner возвращает заметки владельца, новые сверху (индекс по owner_id —
// в миграции).
func (r *NoteRepo) ListByOwner(ctx context.Context, ownerID int64) ([]domain.Note, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, owner_id, title, body, due_date, created_at
		 FROM notes WHERE owner_id = $1
		 ORDER BY created_at DESC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("select notes: %w", err)
	}
	defer rows.Close()

	var notes []domain.Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// Delete удаляет заметку владельца по id. Чужую не трогает — отдаёт ErrNotFound,
// скрывая существование чужих заметок: условие owner_id прямо в DELETE, а
// «была ли строка» говорит RowsAffected.
func (r *NoteRepo) Delete(ctx context.Context, id, ownerID int64) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notes WHERE id = $1 AND owner_id = $2`, id, ownerID)
	if err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound{Entity: "note", ID: id}
	}
	return nil
}

// scanNote читает одну строку заметки (общий скан для списков и Get).
func scanNote(rows *sql.Rows) (domain.Note, error) {
	var n domain.Note
	var due sql.NullString
	if err := rows.Scan(&n.ID, &n.OwnerID, &n.Title, &n.Body, &due, &n.CreatedAt); err != nil {
		return domain.Note{}, fmt.Errorf("scan note: %w", err)
	}
	n.DueDate = due.String // NULL → "" (в домене пустая строка = срока нет)
	return n, nil
}

// toNullString: пустая строка домена → NULL в базе.
func toNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
