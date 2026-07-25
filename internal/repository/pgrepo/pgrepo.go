// Package pgrepo — реализация портов usecase поверх PostgreSQL (database/sql).
//
// ОБРАЗЕЦ ДЛЯ ГЕНЕРАЦИИ: на каждую сущность — свой репозиторий, реализующий
// порт из usecase. Это обычный PostgreSQL:
//
//   - Плейсхолдеры — $1, $2, … (по порядку аргументов).
//   - Новая строка — `INSERT ... RETURNING id` (id выдаёт база: колонка
//     bigint GENERATED ALWAYS AS IDENTITY).
//   - Уникальность — UNIQUE-ограничением в схеме; нарушение ловится
//     isUniqueViolation(err) и переводится в доменную ошибку.
//   - Время — time.Time напрямую (колонки timestamptz), всегда .UTC().
//     NULL в колонке = «не было» — сканируй в sql.NullTime.
//   - «А была ли строка?» после UPDATE/DELETE — через RowsAffected.
//   - Несколько связанных чтений/записей — в транзакции: tx, err :=
//     db.BeginTx(ctx, nil); ... tx.Commit().
package pgrepo

import (
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation — ошибка нарушения UNIQUE-ограничения (код 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// nullTime → time.Time (нулевое время, если NULL).
func fromNullTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time.UTC()
}

// time.Time → значение для записи в timestamptz-колонку с семантикой «NULL
// если не было» (нулевое время пишем как NULL, не как 1970 год).
func toNullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}
