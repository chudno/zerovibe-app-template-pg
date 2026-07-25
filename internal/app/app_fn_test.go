package app_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chudno/zerovibe/internal/app"
	pg "github.com/chudno/zerovibe/internal/platform/pg"
	"github.com/chudno/zerovibe/internal/platform/ycf"
)

// TestFunctionShape — ПРОД-ФОРМА приложения: полная сборка app.Build (реальный
// PostgreSQL) обёрнута адаптером функции, как в serverless-рантайме. Ловит
// расхождения «локально работает, в функции 404/500». Без локального PG — пропуск.
func TestFunctionShape(t *testing.T) {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" && !strings.Contains(dsn, "localhost") && !strings.Contains(dsn, "127.0.0.1") {
		t.Skip("облачный Postgres (песочница) — только локальный прогон")
	}
	if c, err := net.DialTimeout("tcp", "localhost:5432", 300*time.Millisecond); err != nil {
		t.Skip("нет локального Postgres (make pg-up)")
	} else {
		c.Close()
	}
	// Отдельная база app для прод-формы (Open без DATABASE_URL целится в неё).
	admin, err := pg.OpenDSN(context.Background(), "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable")
	if err != nil {
		t.Fatalf("подключение к Postgres: %v", err)
	}
	_, _ = admin.SQL.ExecContext(context.Background(), `CREATE DATABASE app`)
	admin.Close()
	h, cleanup, err := app.Build(context.Background())
	if err != nil {
		t.Fatalf("app.Build: %v", err)
	}
	defer cleanup()
	fn := ycf.Wrap(h)

	cases := []struct {
		event    string
		wantCode float64
	}{
		{`{"httpMethod":"GET","url":"/healthz"}`, 200},
		{`{"httpMethod":"GET","url":"/login"}`, 200},
		{`{"httpMethod":"GET","url":"/"}`, 200},
		{`{"httpMethod":"GET","url":"/nope-nope"}`, 404},
	}
	for _, tc := range cases {
		out, err := fn(context.Background(), []byte(tc.event))
		if err != nil {
			t.Fatalf("%s: %v", tc.event, err)
		}
		var r map[string]any
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("%s: ответ не JSON: %v", tc.event, err)
		}
		if r["statusCode"].(float64) != tc.wantCode {
			body := ""
			if b, ok := r["body"].(string); ok {
				d, _ := base64.StdEncoding.DecodeString(b)
				body = string(d[:min(len(d), 120)])
			}
			t.Errorf("%s → %v (ждали %v); тело: %s", tc.event, r["statusCode"], tc.wantCode, body)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
