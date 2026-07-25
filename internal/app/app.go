// Package app — composition root приложения: читает конфиг из окружения,
// собирает слои (pg → repository → usecase → transport) и отдаёт готовый
// http.Handler. Единственное место, где слои «склеиваются».
//
// Два потребителя одной сборки:
//   - cmd/server — локальный HTTP-сервер (разработка, песочница агента);
//   - handler.go в корне — вход серверлесс-функции (прод/превью на платформе).
package app

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/chudno/zerovibe/internal/adapter/platformfiles"
	"github.com/chudno/zerovibe/internal/adapter/platformmail"
	"github.com/chudno/zerovibe/internal/admin"
	"github.com/chudno/zerovibe/internal/adminres"
	pg "github.com/chudno/zerovibe/internal/platform/pg"
	pgrepo "github.com/chudno/zerovibe/internal/repository/pgrepo"
	"github.com/chudno/zerovibe/internal/transport/web"
	"github.com/chudno/zerovibe/internal/usecase"
)

// Build собирает приложение целиком и возвращает его HTTP-обработчик и cleanup
// (закрытие подключения к базе). Конфигурация — целиком из окружения.
func Build(ctx context.Context) (http.Handler, func(), error) {
	appBaseURL := env("APP_BASE_URL", "http://localhost:8080")
	cookieName := env("SESSION_COOKIE", "zv_session")
	secureCookie := envBool("SECURE_COOKIE", true)
	// ZV_PREVIEW=1 — приложение запущено в live-превью платформы (cross-site iframe):
	// сессионную cookie надо ставить SameSite=None; Secure, иначе вход не удержится.
	previewMode := envBool("ZV_PREVIEW", false)
	// Платформа подставляет адрес API и сервис-ключ для отправки писем при деплое.
	platformURL := os.Getenv("PLATFORM_API_URL")
	platformKey := os.Getenv("PLATFORM_API_KEY")
	// Первый админ создаётся через POST /setup по одноразовому коду SETUP_TOKEN.
	// Код задаёт и передаёт платформа при деплое (env); локально его можно задать
	// в .env. Это единственный путь — отдельного сида из env-кредов нет.
	setupToken := os.Getenv("SETUP_TOKEN")

	// База приложения — PostgreSQL (своя база на приложение; env DATABASE_URL — см.
	// internal/platform/pg). Миграции применяются на старте.
	database, err := pg.Open(ctx)
	if err != nil {
		return nil, nil, err
	}
	cleanup := database.Close
	if err := database.MigrateUp(ctx); err != nil {
		cleanup()
		return nil, nil, err
	}

	// repository
	userRepo := pgrepo.NewUserRepo(database.SQL)
	sessRepo := pgrepo.NewSessionRepo(database.SQL)
	resetRepo := pgrepo.NewResetRepo(database.SQL)
	verifyRepo := pgrepo.NewEmailVerificationRepo(database.SQL)
	rlRepo := pgrepo.NewRateLimitRepo(database.SQL)
	settingRepo := pgrepo.NewSettingRepo(database.SQL)

	// adapters
	mailer := platformmail.New(platformURL, platformKey)
	// Файловое хранилище: прод — presigned-URL платформы (S3); локально —
	// каталог uploads, раздаётся как /uploads/*.
	files := platformfiles.New(platformURL, platformKey, env("UPLOADS_DIR", "uploads"))
	hasher := usecase.NewBcryptHasher()

	// usecase
	settings := usecase.NewSettingsService(settingRepo)
	auth := usecase.NewAuthService(
		userRepo, sessRepo, resetRepo, verifyRepo, rlRepo,
		hasher, mailer, settings,
		usecase.AuthConfig{
			SessionTTL:      30 * 24 * time.Hour,
			ResetTTL:        time.Hour,
			VerifyTTL:       24 * time.Hour,
			AppBaseURL:      appBaseURL,
			LoginRateLimit:  usecase.RateRule{Limit: 5, Window: 15 * time.Minute},
			ForgotRateLimit: usecase.RateRule{Limit: 3, Window: time.Hour},
			ResendShortRate: usecase.RateRule{Limit: 1, Window: time.Minute},
			ResendHourRate:  usecase.RateRule{Limit: 5, Window: time.Hour},
			SetupToken:      setupToken,
		},
	)

	// Первый администратор создаётся через /setup по коду SETUP_TOKEN: код задаёт и
	// передаёт платформа при деплое (env), приложение его только читает. /setup
	// работает, только пока админа ещё нет — после создания первого закрывается.
	if needed, err := auth.SetupNeeded(ctx); err != nil {
		cleanup()
		return nil, nil, err
	} else if needed {
		log.Print("ПЕРВИЧНАЯ НАСТРОЙКА: админ ещё не создан. Создайте его вызовом POST /setup " +
			"с полями email, password и кодом настройки (SETUP_TOKEN).")
	}

	// transport
	srv, err := web.NewServer(auth, settings, web.Config{
		SecureCookie: secureCookie,
		CookieName:   cookieName,
		PreviewMode:  previewMode,
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}

	// Встроенная админка: реестр сущностей + generic-CRUD. Добавить новую сущность
	// в админку = одна строка adminres.RegisterX(reg, repo) здесь (полный рабочий
	// образец всех слоёв — skill new-feature, examples/note).
	reg := admin.NewRegistry()
	adminres.RegisterUser(reg, userRepo, hasher)
	adminSrv, err := admin.NewServer(reg, files)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	srv.SetAdmin(adminSrv)

	return srv.Routes(), cleanup, nil
}

// env возвращает значение переменной окружения или fallback.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envBool парсит булеву переменную окружения; иначе fallback.
func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	switch v {
	case "1", "yes", "on":
		return true
	case "0", "no", "off":
		return false
	}
	return fallback
}
