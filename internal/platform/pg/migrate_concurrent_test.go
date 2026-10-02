package pg_test

// Одновременные старты инстансов не должны выжигать лимит соединений роли.
//
// Прод-инцидент 19 авг 2026: приложение легло с «too many connections for
// role». Причина — миграции на КАЖДОМ старте брали advisory lock БЕЗ
// таймаута, а замороженный серверлесс-инстанс держал лок и соединение. Все
// остальные вставали в вечную очередь и занимали квоту роли (10 соединений).
//
// Тест работает на настоящей PostgreSQL: ни лимит роли, ни advisory-локи на
// моках не воспроизводятся.

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	pg "github.com/chudno/zerovibe/internal/platform/pg"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const roleConnLimit = 10

// adminDSN — DSN суперпользователя тестовой базы.
func adminDSN() string {
	if v := os.Getenv("TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://postgres:postgres@localhost:55433/postgres?sslmode=disable"
}

// seedRole создаёт роль с лимитом соединений и свою базу — как у приложения в
// проде (роль app_<projectID>, лимит 10).
func seedRole(t *testing.T) string {
	t.Helper()
	// Без тестовой базы — пропуск, как у остальных интеграционных тестов: в
	// поде агента её нет, и красный go test ./... останавливал публикацию.
	if os.Getenv("TEST_DATABASE_URL") == "" {
		c, err := net.DialTimeout("tcp", "localhost:55433", 300*time.Millisecond)
		if err != nil {
			t.Skip("нет тестового Postgres (localhost:55433 или TEST_DATABASE_URL) — пропускаю интеграционный тест")
		}
		_ = c.Close()
	}
	admin, err := sql.Open("pgx", adminDSN())
	if err != nil {
		t.Fatalf("админ-подключение: %v", err)
	}
	defer admin.Close()

	role := fmt.Sprintf("app_test_%d", time.Now().UnixNano()%1_000_000)
	ctx := context.Background()
	for _, q := range []string{
		fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, role),
		fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role),
		fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'secret' CONNECTION LIMIT %d`, role, roleConnLimit),
		fmt.Sprintf(`CREATE DATABASE %s OWNER %s`, role, role),
	} {
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatalf("подготовка роли (%s): %v", q, err)
		}
	}
	t.Cleanup(func() {
		c, err := sql.Open("pgx", adminDSN())
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, role))
		_, _ = c.Exec(fmt.Sprintf(`DROP ROLE IF EXISTS %s`, role))
	})
	return fmt.Sprintf("postgres://%s:secret@localhost:55433/%s?sslmode=disable", role, role)
}

// TestMigrate_ConcurrentStartsDoNotExhaustConnections — ГЛАВНЫЙ тест: ровно
// прод-сценарий. Много инстансов стартуют разом; каждый открывает пул и зовёт
// MigrateUp. Никто не должен упереться в лимит роли.
func TestMigrate_ConcurrentStartsDoNotExhaustConnections(t *testing.T) {
	dsn := seedRole(t)

	// Первый старт накатывает схему — как самый первый деплой приложения.
	first, err := pg.OpenDSN(context.Background(), dsn)
	if err != nil {
		t.Fatalf("первый старт: %v", err)
	}
	if err := first.MigrateUp(context.Background()); err != nil {
		t.Fatalf("первые миграции: %v", err)
	}
	first.Close()

	// Дальше — наплыв: инстансов заметно больше, чем позволяет лимит роли
	// поделить между пулами. Так и было в проде.
	const instances = 12
	var wg sync.WaitGroup
	errs := make(chan error, instances)
	for i := 0; i < instances; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			db, err := pg.OpenDSN(ctx, dsn)
			if err != nil {
				errs <- fmt.Errorf("старт инстанса: %w", err)
				return
			}
			defer db.Close()
			if err := db.MigrateUp(ctx); err != nil {
				errs <- fmt.Errorf("миграции инстанса: %w", err)
				return
			}
			// Инстанс обслуживает запрос — обычная работа после старта.
			var one int
			if err := db.SQL.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
				errs <- fmt.Errorf("запрос после старта: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errs)

	var failed []error
	for e := range errs {
		failed = append(failed, e)
	}
	if len(failed) > 0 {
		t.Fatalf("%d из %d инстансов не поднялись (в проде это «приложение просыпается» навсегда).\nПервая ошибка: %v",
			len(failed), instances, failed[0])
	}
}

// TestMigrate_NoPendingSkipsLock — быстрый путь: когда накатывать нечего,
// лок не берётся вовсе. Именно это убирает борьбу за лок на обычных стартах.
func TestMigrate_NoPendingSkipsLock(t *testing.T) {
	dsn := seedRole(t)
	db, err := pg.OpenDSN(context.Background(), dsn)
	if err != nil {
		t.Fatalf("открытие: %v", err)
	}
	defer db.Close()
	if err := db.MigrateUp(context.Background()); err != nil {
		t.Fatalf("первые миграции: %v", err)
	}

	// Держим лок мигратора чужой сессией — как замороженный инстанс в проде.
	holder, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("held: %v", err)
	}
	defer holder.Close()
	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatalf("held conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(),
		`SELECT pg_advisory_lock($1)`, int64(0x7A65726F76696265)); err != nil {
		t.Fatalf("взять лок: %v", err)
	}

	// Миграций нет → старт обязан пройти БЫСТРО, не дожидаясь чужого лока.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.MigrateUp(ctx); err != nil {
		t.Fatalf("старт при занятом локе должен проходить: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("старт ждал лок %v — быстрый путь не сработал (в проде такие ожидания и съели квоту)", d)
	}
}
