// Аутентификация: регистрация, вход/выход, сессии, восстановление пароля и защита
// рейт-лимитами. Слой usecase — порты (интерфейсы хранилищ и адаптеров) + оркестрация.
// bcrypt живёт здесь: хеширование пароля — деталь алгоритма аутентификации, а не
// хранилища и не транспорта; domain при этом остаётся чистым (валидирует плейн-пароль).
package usecase

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/chudno/zerovibe/internal/domain"
)

// --- порты ---

// UserRepository — порт хранилища пользователей.
type UserRepository interface {
	Create(ctx context.Context, u domain.User) (domain.User, error) // ErrEmailTaken при дубле email
	ByEmail(ctx context.Context, email string) (domain.User, error) // ErrNotFound если нет
	ByID(ctx context.Context, id int64) (domain.User, error)
	UpdatePasswordHash(ctx context.Context, userID int64, hash string) error
	MarkEmailVerified(ctx context.Context, userID int64, at time.Time) error
	CountAdmins(ctx context.Context) (int, error) // для сида первого админа
}

// EmailVerificationRepository — порт хранилища токенов подтверждения почты.
type EmailVerificationRepository interface {
	Create(ctx context.Context, v domain.EmailVerification) error
	ByToken(ctx context.Context, token string) (domain.EmailVerification, error)
	MarkUsed(ctx context.Context, token string, usedAt time.Time) error
	DeleteByUser(ctx context.Context, userID int64) error
}

// SessionRepository — порт хранилища сессий.
type SessionRepository interface {
	Create(ctx context.Context, s domain.Session) error
	ByToken(ctx context.Context, token string) (domain.Session, error) // ErrNotFound если нет
	Delete(ctx context.Context, token string) error
	DeleteByUser(ctx context.Context, userID int64) error
	DeleteExpired(ctx context.Context, now time.Time) error
}

// ResetRepository — порт хранилища токенов сброса пароля.
type ResetRepository interface {
	Create(ctx context.Context, p domain.PasswordReset) error
	ByToken(ctx context.Context, token string) (domain.PasswordReset, error)
	MarkUsed(ctx context.Context, token string, usedAt time.Time) error
	DeleteByUser(ctx context.Context, userID int64) error
}

// Email — письмо для отправки через Mailer.
type Email struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer — порт отправки писем (реализация в adapter/platformmail; в тестах — фейк).
type Mailer interface {
	Send(ctx context.Context, m Email) error
}

// RateLimiter — порт оконных счётчиков. Allow атомарно учитывает попытку по ключу и
// сообщает, не превышен ли лимит (и сколько ждать до сброса окна).
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (allowed bool, retryAfter time.Duration, err error)
}

// PasswordHasher — порт хеширования пароля (чтобы тесты не платили цену bcrypt-cost).
type PasswordHasher interface {
	Hash(plain string) (string, error)
	Compare(hash, plain string) error // nil если совпало; domain.ErrInvalidCredentials если нет
}

// SignupPolicy — провайдер флага открытой регистрации (реализуется SettingsService).
// Через него Register узнаёт, разрешена ли регистрация прямо сейчас (настройка
// меняется в рантайме админом).
type SignupPolicy interface {
	Bool(ctx context.Context, key string) (bool, error)
}

// --- bcrypt-реализация PasswordHasher ---

type bcryptHasher struct{ cost int }

// NewBcryptHasher — продакшн-реализация PasswordHasher.
func NewBcryptHasher() PasswordHasher { return bcryptHasher{cost: bcrypt.DefaultCost} }

func (b bcryptHasher) Hash(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), b.cost)
	return string(h), err
}

func (b bcryptHasher) Compare(hash, plain string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return domain.ErrInvalidCredentials
	}
	return err
}

// dummyHash — валидный bcrypt-хеш заглушки. Используется для выравнивания времени
// ответа при несуществующем пользователе (иначе быстрый отказ выдаёт отсутствие email).
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("zerovibe-timing-equalizer"), bcrypt.DefaultCost)

// --- конфиг и сервис ---

// RateRule — правило рейт-лимита: не более Limit попыток за Window.
type RateRule struct {
	Limit  int
	Window time.Duration
}

// AuthConfig — настройки бизнес-правил аутентификации.
type AuthConfig struct {
	SessionTTL      time.Duration
	ResetTTL        time.Duration
	VerifyTTL       time.Duration // срок жизни токена подтверждения почты
	AppBaseURL      string        // для ссылки в письме сброса (без хвостового /)
	LoginRateLimit  RateRule
	ForgotRateLimit RateRule
	// Повторная отправка письма подтверждения: два окна (короткое — пауза между
	// письмами, длинное — часовой потолок), оба должны разрешать.
	ResendShortRate RateRule
	ResendHourRate  RateRule
	// SetupToken — код первичной настройки для создания первого админа через /setup.
	// Задаётся снаружи (env SETUP_TOKEN, передаётся плагином при деплое). Пустой →
	// /setup недоступен. Работает только пока в системе нет ни одного админа.
	SetupToken string
}

// AuthService оркеструет регистрацию/вход/сессии/сброс пароля/подтверждение почты.
type AuthService struct {
	users         UserRepository
	sessions      SessionRepository
	resets        ResetRepository
	verifications EmailVerificationRepository
	rl            RateLimiter
	hasher        PasswordHasher
	mailer        Mailer
	settings      SignupPolicy
	cfg           AuthConfig
	now           func() time.Time // подменяется в тестах
}

// NewAuthService собирает сервис аутентификации.
func NewAuthService(
	users UserRepository, sessions SessionRepository, resets ResetRepository,
	verifications EmailVerificationRepository, rl RateLimiter, hasher PasswordHasher,
	mailer Mailer, settings SignupPolicy, cfg AuthConfig,
) *AuthService {
	return &AuthService{
		users: users, sessions: sessions, resets: resets, verifications: verifications,
		rl: rl, hasher: hasher, mailer: mailer, settings: settings, cfg: cfg, now: time.Now,
	}
}

// Ключи настроек, влияющих на аутентификацию.
const (
	signupAllowKey   = "allow_signup"
	requireVerifyKey = "require_email_verification"
)

// Register создаёт обычного пользователя. Если регистрация закрыта → ErrSignupClosed.
// Если включено подтверждение почты — выпускает токен и шлёт письмо со ссылкой.
func (s *AuthService) Register(ctx context.Context, email, password string) (domain.User, error) {
	allowed, err := s.settings.Bool(ctx, signupAllowKey)
	if err != nil {
		return domain.User{}, err
	}
	if !allowed {
		return domain.User{}, domain.ErrSignupClosed
	}
	u, err := domain.NewUser(email, password, domain.RoleUser)
	if err != nil {
		return domain.User{}, err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return domain.User{}, fmt.Errorf("hash password: %w", err)
	}
	u.PasswordHash = hash
	created, err := s.users.Create(ctx, u)
	if err != nil {
		// Дубль email → ErrEmailTaken пробрасывается наружу как есть: форма честно
		// сообщает «email уже занят». Это раскрывает наличие аккаунта (user
		// enumeration), но для приложения такого класса принято осознанно: регистрация
		// по умолчанию закрыта (перебирать некому), а при открытой публичной
		// регистрации привычное «email занят» важнее сокрытия факта (как на большинстве
		// сайтов). Вход/сброс/повторное письмо при этом анти-enumeration — там скрытие
		// действительно критично.
		return domain.User{}, err
	}

	// Если требуется подтверждение почты — выпускаем токен и шлём письмо.
	if require, _ := s.settings.Bool(ctx, requireVerifyKey); require {
		_ = s.issueAndSendVerification(ctx, created)
	}
	return created, nil
}

// Login проверяет креды и создаёт сессию. rateKey формирует транспорт (email+IP).
func (s *AuthService) Login(ctx context.Context, email, password, rateKey string) (domain.Session, error) {
	now := s.now()
	allowed, retry, err := s.rl.Allow(ctx, "login:"+rateKey, s.cfg.LoginRateLimit.Limit, s.cfg.LoginRateLimit.Window, now)
	if err != nil {
		return domain.Session{}, err
	}
	if !allowed {
		return domain.Session{}, domain.ErrRateLimited{RetryAfter: retry}
	}

	u, err := s.users.ByEmail(ctx, domain.NormalizeEmail(email))
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			// Выравниваем время: всё равно прогоняем bcrypt против заглушки, затем
			// отдаём единую ошибку — нельзя отличить «нет email» от «неверный пароль».
			_ = s.hasher.Compare(string(dummyHash), password)
			return domain.Session{}, domain.ErrInvalidCredentials
		}
		return domain.Session{}, err
	}
	if err := s.hasher.Compare(u.PasswordHash, password); err != nil {
		return domain.Session{}, err // ErrInvalidCredentials от hasher
	}

	// Блокируем вход, если требуется подтверждение почты, а она не подтверждена.
	// Проверяем ПОСЛЕ пароля — чтобы не раскрывать статус чужого аккаунта.
	if require, _ := s.settings.Bool(ctx, requireVerifyKey); require && !u.EmailVerified() {
		return domain.Session{}, domain.ErrEmailNotVerified
	}

	token, err := randomToken()
	if err != nil {
		return domain.Session{}, err
	}
	sess := domain.Session{Token: token, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.cfg.SessionTTL)}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return domain.Session{}, err
	}
	return sess, nil
}

// Authenticate по токену сессии возвращает пользователя (для middleware). Истёкшую
// сессию удаляет и сообщает ErrUnauthenticated.
func (s *AuthService) Authenticate(ctx context.Context, token string) (domain.User, error) {
	sess, err := s.sessions.ByToken(ctx, token)
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			return domain.User{}, domain.ErrUnauthenticated
		}
		return domain.User{}, err
	}
	if sess.Expired(s.now()) {
		_ = s.sessions.Delete(ctx, token)
		return domain.User{}, domain.ErrUnauthenticated
	}
	return s.users.ByID(ctx, sess.UserID)
}

// Logout удаляет сессию (идемпотентно: отсутствие токена не ошибка).
func (s *AuthService) Logout(ctx context.Context, token string) error {
	err := s.sessions.Delete(ctx, token)
	var nf domain.ErrNotFound
	if err != nil && errors.As(err, &nf) {
		return nil
	}
	return err
}

// RequestReset инициирует сброс пароля. Анти-enumeration: наружу всегда nil (кроме
// рейт-лимита) — по ответу нельзя узнать, есть ли такой email. Письмо уходит только
// если пользователь существует; ошибку отправки логируем внутри, наружу не отдаём.
func (s *AuthService) RequestReset(ctx context.Context, email, rateKey string) error {
	now := s.now()
	allowed, retry, err := s.rl.Allow(ctx, "forgot:"+rateKey, s.cfg.ForgotRateLimit.Limit, s.cfg.ForgotRateLimit.Window, now)
	if err != nil {
		return err
	}
	if !allowed {
		return domain.ErrRateLimited{RetryAfter: retry}
	}

	u, err := s.users.ByEmail(ctx, domain.NormalizeEmail(email))
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			return nil // молчим: не раскрываем отсутствие email (рейт-лимит уже потрачен)
		}
		return err
	}

	// Инвалидируем прежние токены сброса этого пользователя.
	if err := s.resets.DeleteByUser(ctx, u.ID); err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	pr := domain.PasswordReset{Token: token, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.cfg.ResetTTL)}
	if err := s.resets.Create(ctx, pr); err != nil {
		return err
	}

	link := s.cfg.AppBaseURL + "/reset?token=" + token
	msg := Email{
		To:      u.Email,
		Subject: "Восстановление пароля",
		Text:    "Чтобы задать новый пароль, перейдите по ссылке:\n" + link + "\n\nЕсли вы не запрашивали сброс — просто проигнорируйте письмо.",
		HTML:    `<p>Чтобы задать новый пароль, перейдите по ссылке:</p><p><a href="` + link + `">` + link + `</a></p><p>Если вы не запрашивали сброс — просто проигнорируйте письмо.</p>`,
	}
	// Ошибку отправки не пробрасываем наружу (анти-enumeration + не валим поток).
	_ = s.mailer.Send(ctx, msg)
	return nil
}

// ConfirmReset проверяет токен, меняет пароль, гасит токен и разлогинивает пользователя
// везде (безопасность: после сброса все старые сессии недействительны).
func (s *AuthService) ConfirmReset(ctx context.Context, token, newPassword string) error {
	if err := domain.ValidatePasswordPlain(newPassword); err != nil {
		return err
	}
	pr, err := s.resets.ByToken(ctx, token)
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			return domain.ErrInvalidToken
		}
		return err
	}
	if !pr.Usable(s.now()) {
		return domain.ErrInvalidToken
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := s.users.UpdatePasswordHash(ctx, pr.UserID, hash); err != nil {
		return err
	}
	if err := s.resets.MarkUsed(ctx, token, s.now()); err != nil {
		return err
	}
	return s.sessions.DeleteByUser(ctx, pr.UserID)
}

// SetupNeeded сообщает, доступна ли первичная настройка: задан код SetupToken И в
// системе ещё нет ни одного админа. Используется composition root (вывести подсказку
// в лог) и при необходимости транспортом.
func (s *AuthService) SetupNeeded(ctx context.Context) (bool, error) {
	if s.cfg.SetupToken == "" {
		return false, nil
	}
	n, err := s.users.CountAdmins(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// Setup создаёт ПЕРВОГО администратора по коду первичной настройки (SetupToken,
// заданному снаружи). Код не задан или админ уже есть → ErrSetupClosed. Неверный
// код → ErrSetupToken. После создания админа CountAdmins>0 → /setup закрыт навсегда.
func (s *AuthService) Setup(ctx context.Context, email, password, token string) error {
	if s.cfg.SetupToken == "" {
		return domain.ErrSetupClosed
	}
	n, err := s.users.CountAdmins(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.ErrSetupClosed // админ уже есть — настройка завершена
	}
	// Сравнение в постоянном времени, чтобы код нельзя было подобрать по таймингу.
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.SetupToken)) != 1 {
		return domain.ErrSetupToken
	}

	u, err := domain.NewUser(email, password, domain.RoleAdmin)
	if err != nil {
		return err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	u.PasswordHash = hash
	_, err = s.users.Create(ctx, u)
	return err
}

// issueAndSendVerification выпускает токен подтверждения почты (инвалидируя
// прежние) и отправляет письмо со ссылкой. Ошибку отправки наружу не пробрасываем.
func (s *AuthService) issueAndSendVerification(ctx context.Context, u domain.User) error {
	if err := s.verifications.DeleteByUser(ctx, u.ID); err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	now := s.now()
	v := domain.EmailVerification{Token: token, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.cfg.VerifyTTL)}
	if err := s.verifications.Create(ctx, v); err != nil {
		return err
	}
	link := s.cfg.AppBaseURL + "/verify-email?token=" + token
	msg := Email{
		To:      u.Email,
		Subject: "Подтверждение адреса почты",
		Text:    "Подтвердите адрес почты, перейдя по ссылке:\n" + link + "\n\nЕсли вы не регистрировались — проигнорируйте письмо.",
		HTML:    `<p>Подтвердите адрес почты, перейдя по ссылке:</p><p><a href="` + link + `">` + link + `</a></p><p>Если вы не регистрировались — проигнорируйте письмо.</p>`,
	}
	_ = s.mailer.Send(ctx, msg)
	return nil
}

// ConfirmEmailVerification подтверждает почту по токену: помечает пользователя
// подтверждённым и гасит токен. Невалидный токен → ErrInvalidToken.
func (s *AuthService) ConfirmEmailVerification(ctx context.Context, token string) error {
	v, err := s.verifications.ByToken(ctx, token)
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			return domain.ErrInvalidToken
		}
		return err
	}
	if !v.Usable(s.now()) {
		return domain.ErrInvalidToken
	}
	now := s.now()
	if err := s.users.MarkEmailVerified(ctx, v.UserID, now); err != nil {
		return err
	}
	return s.verifications.MarkUsed(ctx, token, now)
}

// ResendVerification повторно отправляет письмо подтверждения. Рейт-лимит — два окна
// (пауза между письмами + часовой потолок). Анти-enumeration по ОТВЕТУ: наружу всегда
// nil (кроме рейт-лимита) — по коду/телу ответа нельзя узнать, есть ли email и
// подтверждён ли он. Тайминг при этом не выровнен (для существующего неподтверждённого
// идёт отправка письма, для прочих — ранний выход): это осознанный tradeoff для
// приложения такого класса, тайминг-анализ требует заметных усилий атакующего, а
// рейт-лимит (1/мин) делает массовый замер дорогим.
func (s *AuthService) ResendVerification(ctx context.Context, email, rateKey string) error {
	now := s.now()
	if ok, retry, err := s.rl.Allow(ctx, "resend-short:"+rateKey, s.cfg.ResendShortRate.Limit, s.cfg.ResendShortRate.Window, now); err != nil {
		return err
	} else if !ok {
		return domain.ErrRateLimited{RetryAfter: retry}
	}
	if ok, retry, err := s.rl.Allow(ctx, "resend-hour:"+rateKey, s.cfg.ResendHourRate.Limit, s.cfg.ResendHourRate.Window, now); err != nil {
		return err
	} else if !ok {
		return domain.ErrRateLimited{RetryAfter: retry}
	}

	u, err := s.users.ByEmail(ctx, domain.NormalizeEmail(email))
	if err != nil {
		var nf domain.ErrNotFound
		if errors.As(err, &nf) {
			return nil // молчим: не раскрываем отсутствие email
		}
		return err
	}
	if u.EmailVerified() {
		return nil // уже подтверждена — письмо не нужно, но и не выдаём этот факт
	}
	return s.issueAndSendVerification(ctx, u)
}

// randomToken — 32 байта crypto/rand в hex (64 символа). Для сессий и токенов сброса.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
