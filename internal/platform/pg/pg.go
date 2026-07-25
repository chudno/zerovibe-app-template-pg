// Package pg — подключение приложения к PostgreSQL и миграции схемы.
//
// БАЗА-НА-ПРИЛОЖЕНИЕ: приложение получает СВОЮ базу (и свою роль) через одну
// переменную окружения:
//
//	DATABASE_URL — postgres://role:pass@host:6432/dbname?sslmode=require
//	               (локально: postgres://postgres:postgres@localhost:5432/app?sslmode=disable)
//
// Работа через database/sql (стандартный интерфейс Go) поверх драйвера pgx.
// Плейсхолдеры — $1, $2, …; поддерживаются RETURNING, UNIQUE-ограничения,
// SERIAL/IDENTITY — обычный PostgreSQL без экзотики.
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql драйвер "pgx"
)

// DB — открытое подключение приложения к базе.
type DB struct {
	SQL *sql.DB
}

// Open подключается по DATABASE_URL (см. package doc). Блокирует до готовности
// соединения или таймаута — приложение без базы бессмысленно, лучше упасть
// сразу с понятной ошибкой.
func Open(ctx context.Context) (*DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://postgres:postgres@localhost:5432/app?sslmode=disable"
	}
	return OpenDSN(ctx, dsn)
}

// OpenDSN — как Open, но с явным DSN (нужен тестам: схема-на-тест через
// параметр search_path в DSN).
func OpenDSN(ctx context.Context, dsn string) (*DB, error) {
	// Кластер приложений отдаёт соединения через пулер (Odyssey, transaction
	// mode) — prepared statements там ломаются («prepared statement … does not
	// exist»): пулер переключает бэкенды между транзакциями. simple_protocol
	// отключает кэш подготовленных выражений pgx; безопасен и локально.
	if !strings.Contains(dsn, "default_query_exec_mode=") {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + "default_query_exec_mode=simple_protocol"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("pg open: %w", err)
	}
	// Кластер приложений маленький, соединения дефицитны (пулер на стороне
	// сервера) — держим скромный пул.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pg ping: %w", err)
	}
	return &DB{SQL: db}, nil
}

// Close закрывает подключение.
func (d *DB) Close() { _ = d.SQL.Close() }
