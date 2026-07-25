// Admin-методы репозитория заметок — для встроенной админ-панели (generic-CRUD).
// Живут в том же файле-пакете repository/pgrepo, отдельным файлом <сущность>_admin.go.
package pgrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/chudno/zerovibe/internal/domain"
)

// ListAll возвращает все заметки (новые сверху) — источник списка для админки.
func (r *NoteRepo) ListAll(ctx context.Context) ([]domain.Note, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, owner_id, title, body, due_date, created_at
		 FROM notes ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("select all notes: %w", err)
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

// GetByID возвращает заметку по id (форма редактирования в админке).
func (r *NoteRepo) GetByID(ctx context.Context, id int64) (domain.Note, error) {
	var n domain.Note
	var due sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT id, owner_id, title, body, due_date, created_at FROM notes WHERE id = $1`, id).
		Scan(&n.ID, &n.OwnerID, &n.Title, &n.Body, &due, &n.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Note{}, domain.ErrNotFound{Entity: "note", ID: id}
	}
	if err != nil {
		return domain.Note{}, fmt.Errorf("get note: %w", err)
	}
	n.DueDate = due.String
	return n, nil
}

// UpdateAny обновляет заметку БЕЗ проверки владельца (админ правит любые).
func (r *NoteRepo) UpdateAny(ctx context.Context, n domain.Note) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE notes SET title = $1, body = $2, due_date = $3 WHERE id = $4`,
		n.Title, n.Body, toNullString(n.DueDate), n.ID)
	if err != nil {
		return fmt.Errorf("update note: %w", err)
	}
	return errIfNoRowsNote(res, n.ID)
}

// DeleteAny удаляет заметку БЕЗ проверки владельца (админ удаляет любые).
func (r *NoteRepo) DeleteAny(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM notes WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete note: %w", err)
	}
	return errIfNoRowsNote(res, id)
}

// errIfNoRowsNote — ErrNotFound, если запрос не задел ни одной строки.
func errIfNoRowsNote(res sql.Result, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound{Entity: "note", ID: id}
	}
	return nil
}
