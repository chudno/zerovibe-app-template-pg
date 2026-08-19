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
//
// ВАЖНО про серверлесс (инцидент 19 авг 2026, приложение легло в прод):
// инстанс после ответа НЕ УМИРАЕТ, а замораживается — соединение остаётся
// открытым, и взятый им advisory lock остаётся за ним. Пока он держит лок,
// остальные инстансы ждут ВЕЧНО и копят соединения: пул 4 на инстанс при
// лимите роли 10 → три инстанса выжигают квоту, дальше «too many connections
// for role» и приложение не поднимается вовсе.
//
// Поэтому здесь два предохранителя:
//  1. быстрая проверка БЕЗ лока — накатывать нечего (обычный случай: схема
//     давно применена) → выходим сразу, за лок никто не борется;
//  2. лок с таймаутом (pg_try_advisory_lock в цикле) вместо вечного
//     ожидания: не дождались — не падаем, а идём дальше (значит миграции
//     катит другой инстанс, и схема вот-вот будет готова).
package pg

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"sort"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrateLockID — ключ advisory lock мигратора (случайная константа приложения).
const migrateLockID = 0x7A65726F76696265 // "zerovibe"

// migrationNames — имена файлов миграций по порядку.
func migrationNames() ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("migrate: read dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// pendingMigrations — что ещё не применено. Один дешёвый SELECT, без лока и
// без выделенного соединения: обычный старт должен стоить ровно этого.
//
// Ошибка (нет таблицы schema_migrations — первый запуск) НЕ фатальна: вызов
// трактует её как «надо идти под локом», и там всё создастся штатно.
func (d *DB) pendingMigrations(ctx context.Context) ([]string, error) {
	names, err := migrationNames()
	if err != nil {
		return nil, err
	}
	rows, err := d.SQL.QueryContext(ctx, `SELECT id FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		applied[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !applied[n] {
			out = append(out, n)
		}
	}
	return out, nil
}

// tryLock берёт лок мигратора, но ждёт не дольше lockWaitTimeout.
//
// pg_try_advisory_lock не блокирует вовсе (сразу true/false), поэтому ждём
// сами короткими паузами. Так ожидающий не висит в базе бесконечно, занимая
// одно из немногих соединений роли.
func tryLock(ctx context.Context, conn *sql.Conn) (bool, error) {
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		var ok bool
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, migrateLockID).Scan(&ok); err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// lockWaitTimeout — сколько ждём лок мигратора. Не дождались → не падаем:
// миграции катит другой инстанс. Держать очередь дольше нельзя — ждущие
// занимают соединения, которых у роли всего десяток.
const lockWaitTimeout = 5 * time.Second

// MigrateUp применяет непройденные миграции. Идемпотентен — зовётся на каждом
// старте приложения.
func (d *DB) MigrateUp(ctx context.Context) error {
	// Быстрый путь без лока: накатывать нечего — не трогаем блокировку вовсе.
	// Это обычный случай (схема применена давным-давно), и именно он не должен
	// стоить ни лока, ни очереди.
	if pending, err := d.pendingMigrations(ctx); err == nil && len(pending) == 0 {
		return nil
	}

	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrate: conn: %w", err)
	}
	defer conn.Close()

	// Сериализация конкурентных стартов. Лок с ТАЙМАУТОМ: вечное ожидание в
	// серверлессе выжигает лимит соединений роли (см. док пакета).
	locked, err := tryLock(ctx, conn)
	if err != nil {
		return fmt.Errorf("migrate: advisory lock: %w", err)
	}
	if !locked {
		// Лок держит другой инстанс — он и накатит. Продолжаем старт: схема
		// либо уже готова, либо будет через мгновение. Падать здесь нельзя:
		// это отдало бы пользователю ошибку на ровном месте.
		slog.Info("миграции применяет другой инстанс, пропускаем")
		return nil
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

	names, err := migrationNames()
	if err != nil {
		return err
	}

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
