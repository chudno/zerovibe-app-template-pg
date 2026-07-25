// Миграции схемы — минимальный встроенный мигратор: файлы
// migrations/NNNNN_*.sql применяются по порядку, применённые помечаются
// строкой в таблице schema_migrations. Новая фича = новый файл.
//
// Правила файлов миграций (ВАЖНО для генерации):
//   - обычный PostgreSQL DDL; несколько операторов разделяются `;`;
//   - каждая миграция применяется В ОДНОЙ ТРАНЗАКЦИИ (упала — откатилась
//     целиком, полумиграций не бывает);
//   - откатов (down) нет — только вперёд.
//
// Конкурентные старты (несколько инстансов серверлесс-функции разом) не
// мешают друг другу: advisory lock сериализует мигратор на стороне базы.
package pg

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sort"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrateLockID — ключ advisory lock мигратора (случайная константа приложения).
const migrateLockID = 0x7A65726F76696265 // "zerovibe"

// MigrateUp применяет непройденные миграции. Идемпотентен — зовётся на каждом
// старте приложения.
func (d *DB) MigrateUp(ctx context.Context) error {
	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: conn: %w", err)
	}
	defer conn.Close()

	// Сериализация конкурентных стартов: блокировка живёт до конца сессии.
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrateLockID); err != nil {
		return fmt.Errorf("migrate: advisory lock: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, migrateLockID) }()

	if _, err := conn.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (id text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, `SELECT id FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("migrate: list applied: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		applied[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("migrate: read dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("migrate: read %s: %w", name, err)
		}
		// Миграция целиком + отметка — одна транзакция: или всё, или ничего.
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate: begin %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (id) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: mark %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate: commit %s: %w", name, err)
		}
		slog.Info("миграция применена", "migration", name)
	}
	return nil
}
