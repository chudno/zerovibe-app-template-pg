// E2E-тесты транспорта на полном стеке (локальный PostgreSQL, схема на тест).
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	pg "github.com/chudno/zerovibe/internal/platform/pg"
	pgrepo "github.com/chudno/zerovibe/internal/repository/pgrepo"
	"github.com/chudno/zerovibe/internal/usecase"
)

// noMailer — заглушка Mailer для тестов транспорта (письма тут не нужны).
type noMailer struct{}

func (noMailer) Send(_ context.Context, _ usecase.Email) error { return nil }

// capturedToken — токен из последнего письма сброса (для e2e reset-флоу).
var capturedToken string

// captureMailer достаёт токен сброса из ссылки в тексте письма.
type captureMailer struct{}

var resetLinkRe = regexp.MustCompile(`/reset\?token=([0-9a-f]+)`)
var verifyLinkRe = regexp.MustCompile(`/verify-email\?token=([0-9a-f]+)`)

// capturedVerifyToken — токен из последнего письма подтверждения почты.
var capturedVerifyToken string

func (captureMailer) Send(_ context.Context, m usecase.Email) error {
	if mm := resetLinkRe.FindStringSubmatch(m.Text); mm != nil {
		capturedToken = mm[1]
	}
	if mm := verifyLinkRe.FindStringSubmatch(m.Text); mm != nil {
		capturedVerifyToken = mm[1]
	}
	return nil
}

// buildStackWithMailer — как buildStack, но с мейлером-перехватчиком токена сброса.
// testSetupToken — код первичной настройки, заданный в тестовом стеке. Совпадает с
// тем, что в проде задаёт плагин через env SETUP_TOKEN. /setup доступен, только пока
// в системе нет ни одного админа.
const testSetupToken = "test-setup-token"

// openTestPG подключается к локальному PostgreSQL (make pg-up) и даёт тесту
// СВОЮ схему (изоляция без отдельной базы). Нет локального Postgres — тест
// пропускается: юнит-тесты не должны требовать докера, полный прогон — make check.
//
// ТОЛЬКО ЛОКАЛЬНЫЙ POSTGRES: против облачного кластера (DATABASE_URL песочницы
// в поде агента) интеграционные тесты НЕ гоняются — общая база не место для
// тестового мусора. Живую проверку в песочнице агент делает скиллом run,
// полный прогон тестов — локальная разработка/CI.
func openTestPG(t *testing.T) *pg.DB {
	t.Helper()
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" && !strings.Contains(dsn, "localhost") && !strings.Contains(dsn, "127.0.0.1") {
		t.Skip("облачный Postgres (песочница) — интеграционные тесты гоняются только против локального")
	}
	if c, err := net.DialTimeout("tcp", "localhost:5432", 300*time.Millisecond); err != nil {
		t.Skip("нет локального Postgres (make pg-up) — пропускаю интеграционный тест")
	} else {
		c.Close()
	}

	var sub [8]byte
	if _, err := rand.Read(sub[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	schema := "test_" + hex.EncodeToString(sub[:])

	const base = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	admin, err := pg.OpenDSN(context.Background(), base)
	if err != nil {
		t.Fatalf("открыть Postgres: %v", err)
	}
	if _, err := admin.SQL.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("создать схему: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.SQL.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	database, err := pg.OpenDSN(context.Background(), base+"&search_path="+schema)
	if err != nil {
		t.Fatalf("открыть схему теста: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.MigrateUp(context.Background()); err != nil {
		t.Fatalf("миграции: %v", err)
	}
	return database
}

func buildStackWithMailer(t *testing.T, allowSignup bool) (http.Handler, *usecase.AuthService, *usecase.SettingsService) {
	t.Helper()
	capturedToken = ""
	capturedVerifyToken = ""
	database := openTestPG(t)
	settings := usecase.NewSettingsService(pgrepo.NewSettingRepo(database.SQL))
	// Явно выставляем allow_signup под тест (не полагаемся на дефолт реестра — он
	// теперь true, поэтому «закрытую» регистрацию нужно ставить явно false).
	if allowSignup {
		_ = settings.Set(context.Background(), "allow_signup", "true")
	} else {
		_ = settings.Set(context.Background(), "allow_signup", "false")
	}
	auth := usecase.NewAuthService(
		pgrepo.NewUserRepo(database.SQL), pgrepo.NewSessionRepo(database.SQL),
		pgrepo.NewResetRepo(database.SQL), pgrepo.NewEmailVerificationRepo(database.SQL), pgrepo.NewRateLimitRepo(database.SQL),
		usecase.NewBcryptHasher(), captureMailer{}, settings,
		usecase.AuthConfig{
			SessionTTL:      time.Hour,
			ResetTTL:        time.Hour,
			VerifyTTL:       24 * time.Hour,
			AppBaseURL:      "http://localhost",
			LoginRateLimit:  usecase.RateRule{Limit: 5, Window: 15 * time.Minute},
			ForgotRateLimit: usecase.RateRule{Limit: 3, Window: time.Hour},
			ResendShortRate: usecase.RateRule{Limit: 1, Window: time.Minute},
			ResendHourRate:  usecase.RateRule{Limit: 5, Window: time.Hour},
			SetupToken:      testSetupToken,
		},
	)
	srv, err := NewServer(auth, settings, Config{SecureCookie: false, CookieName: "zv_session"})
	if err != nil {
		t.Fatalf("сервер: %v", err)
	}
	return srv.Routes(), auth, settings
}

// buildStack собирает полный стек на временной БД. Возвращает handler и сервисы для
// сидирования. allowSignup управляет открытой регистрацией.
func buildStack(t *testing.T, allowSignup bool) (http.Handler, *usecase.AuthService, *usecase.SettingsService) {
	t.Helper()
	database := openTestPG(t)

	settings := usecase.NewSettingsService(pgrepo.NewSettingRepo(database.SQL))
	// Явно ставим allow_signup под тест: дефолт реестра теперь true, поэтому
	// «закрытую» регистрацию для тестов надо выставлять явно false.
	val := "false"
	if allowSignup {
		val = "true"
	}
	if err := settings.Set(context.Background(), "allow_signup", val); err != nil {
		t.Fatalf("настройка allow_signup: %v", err)
	}
	auth := usecase.NewAuthService(
		pgrepo.NewUserRepo(database.SQL), pgrepo.NewSessionRepo(database.SQL),
		pgrepo.NewResetRepo(database.SQL), pgrepo.NewEmailVerificationRepo(database.SQL), pgrepo.NewRateLimitRepo(database.SQL),
		usecase.NewBcryptHasher(), noMailer{}, settings,
		usecase.AuthConfig{
			SessionTTL:      time.Hour,
			ResetTTL:        time.Hour,
			VerifyTTL:       24 * time.Hour,
			AppBaseURL:      "http://localhost",
			LoginRateLimit:  usecase.RateRule{Limit: 5, Window: 15 * time.Minute},
			ForgotRateLimit: usecase.RateRule{Limit: 3, Window: time.Hour},
			ResendShortRate: usecase.RateRule{Limit: 1, Window: time.Minute},
			ResendHourRate:  usecase.RateRule{Limit: 5, Window: time.Hour},
			SetupToken:      testSetupToken,
		},
	)
	srv, err := NewServer(auth, settings, Config{SecureCookie: false, CookieName: "zv_session"})
	if err != nil {
		t.Fatalf("сервер: %v", err)
	}
	return srv.Routes(), auth, settings
}

// buildStackPreview собирает стек в режиме превью (PreviewMode=true) — для проверки
// атрибутов сессионной cookie в cross-site iframe (SameSite=None; Secure).
func buildStackPreview(t *testing.T) (http.Handler, *usecase.AuthService) {
	t.Helper()
	database := openTestPG(t)
	settings := usecase.NewSettingsService(pgrepo.NewSettingRepo(database.SQL))
	auth := usecase.NewAuthService(
		pgrepo.NewUserRepo(database.SQL), pgrepo.NewSessionRepo(database.SQL),
		pgrepo.NewResetRepo(database.SQL), pgrepo.NewEmailVerificationRepo(database.SQL), pgrepo.NewRateLimitRepo(database.SQL),
		usecase.NewBcryptHasher(), noMailer{}, settings,
		usecase.AuthConfig{
			SessionTTL: time.Hour, ResetTTL: time.Hour, VerifyTTL: 24 * time.Hour,
			AppBaseURL:      "http://localhost",
			LoginRateLimit:  usecase.RateRule{Limit: 5, Window: 15 * time.Minute},
			ForgotRateLimit: usecase.RateRule{Limit: 3, Window: time.Hour},
			ResendShortRate: usecase.RateRule{Limit: 1, Window: time.Minute},
			ResendHourRate:  usecase.RateRule{Limit: 5, Window: time.Hour},
			SetupToken:      testSetupToken,
		},
	)
	srv, err := NewServer(auth, settings, Config{SecureCookie: false, CookieName: "zv_session", PreviewMode: true})
	if err != nil {
		t.Fatalf("сервер: %v", err)
	}
	return srv.Routes(), auth
}

// seedAdminAndLogin создаёт первого админа (через первичную настройку по тестовому
// коду) и возвращает cookie его сессии. Идёт тем же путём, что и прод — /setup.
func seedAdminAndLogin(t *testing.T, h http.Handler, auth *usecase.AuthService, email, pass string) *http.Cookie {
	t.Helper()
	if err := auth.Setup(context.Background(), email, pass, testSetupToken); err != nil {
		t.Fatalf("сид админа: %v", err)
	}
	return loginCookie(t, h, email, pass)
}

// loginCookie логинится и возвращает cookie сессии.
func loginCookie(t *testing.T, h http.Handler, email, pass string) *http.Cookie {
	t.Helper()
	form := url.Values{"email": {email}, "password": {pass}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == "zv_session" && c.Value != "" {
			return c
		}
	}
	t.Fatalf("логин не вернул cookie сессии (код %d): %s", rec.Code, rec.Body.String())
	return nil
}

// newAuthedServer — стек + cookie залогиненного пользователя (через сид-админа).
func newAuthedServer(t *testing.T) (http.Handler, *http.Cookie) {
	h, auth, _ := buildStack(t, false)
	c := seedAdminAndLogin(t, h, auth, "owner@example.com", "password123")
	return h, c
}

func TestStaticServed(t *testing.T) {
	h, _ := newAuthedServer(t)
	// app.css больше нет (DaisyUI убрали) — проверяем оставшуюся статику: htmx и шрифт.
	for _, name := range []string{"htmx.min.js", "fonts/onest-400.woff2"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/static/"+name, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /static/%s: ожидался 200, получен %d", name, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET /static/%s: пустое тело", name)
		}
	}
}
