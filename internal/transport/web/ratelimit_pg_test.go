// Интеграционные тесты счётчика рейт-лимита (pgrepo.RateLimitRepo) на локальном
// PostgreSQL. Без базы пропускаются (openTestPG). Лежат в пакете web рядом с
// openTestPG, потому что у pgrepo своих тестов нет.
package web

import (
	"context"
	"sync"
	"testing"
	"time"

	pgrepo "github.com/chudno/zerovibe/internal/repository/pgrepo"
)

func TestRateLimitRepo_Reset(t *testing.T) {
	database := openTestPG(t)
	repo := pgrepo.NewRateLimitRepo(database.SQL)
	ctx := context.Background()
	now := time.Now()
	for i := 0; i < 3; i++ {
		if _, _, err := repo.Allow(ctx, "k", 3, time.Hour, now); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _, _ := repo.Allow(ctx, "k", 3, time.Hour, now); ok {
		t.Fatal("четвёртая попытка должна быть заблокирована")
	}
	if err := repo.Reset(ctx, "k"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if ok, _, err := repo.Allow(ctx, "k", 3, time.Hour, now); err != nil || !ok {
		t.Fatalf("после Reset попытка должна пройти: ok=%v err=%v", ok, err)
	}
	if err := repo.Reset(ctx, "нет-такого"); err != nil {
		t.Fatalf("Reset без счётчика не должен быть ошибкой: %v", err)
	}
}

// Параллельные попытки не теряют инкременты: из 30 разрешены ровно 5.
func TestRateLimitRepo_Allow_Concurrent(t *testing.T) {
	database := openTestPG(t)
	repo := pgrepo.NewRateLimitRepo(database.SQL)
	ctx := context.Background()
	now := time.Now()

	const total, limit = 30, 5
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, err := repo.Allow(ctx, "race", limit, time.Hour, now)
			if err != nil {
				t.Errorf("Allow: %v", err)
				return
			}
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowed != limit {
		t.Errorf("разрешено %d попыток, ожидалось ровно %d", allowed, limit)
	}
	var count int64
	if err := database.SQL.QueryRowContext(ctx, `SELECT count FROM rate_counters WHERE key = 'race'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != total {
		t.Errorf("итоговый счётчик %d, ожидалось %d", count, total)
	}
}

// Окно истекло (now-start >= window) — счётчик начинается заново с 1.
func TestRateLimitRepo_Allow_WindowExpiry(t *testing.T) {
	database := openTestPG(t)
	repo := pgrepo.NewRateLimitRepo(database.SQL)
	ctx := context.Background()
	const window = time.Minute
	start := time.Now().UTC().Truncate(time.Millisecond)

	for i := 0; i < 3; i++ {
		if _, _, err := repo.Allow(ctx, "win", 2, window, start); err != nil {
			t.Fatal(err)
		}
	}
	// чуть раньше конца окна — ещё то же окно, отказ с ненулевым retryAfter
	ok, retry, err := repo.Allow(ctx, "win", 2, window, start.Add(window-time.Second))
	if err != nil || ok || retry <= 0 {
		t.Fatalf("до конца окна ожидался отказ с retryAfter>0: ok=%v retry=%v err=%v", ok, retry, err)
	}
	// ровно через window — новое окно
	ok, _, err = repo.Allow(ctx, "win", 2, window, start.Add(window))
	if err != nil || !ok {
		t.Fatalf("после окна попытка должна пройти: ok=%v err=%v", ok, err)
	}
	var count int64
	if err := database.SQL.QueryRowContext(ctx, `SELECT count FROM rate_counters WHERE key = 'win'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("после истечения окна счётчик %d, ожидалось 1", count)
	}
}
