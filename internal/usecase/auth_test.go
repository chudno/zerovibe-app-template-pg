// Unit-тесты аутентификации на фейковых портах (без БД, сети и цены bcrypt).
package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// --- фейки портов ---

type fakeUserRepo struct {
	byID    map[int64]domain.User
	byEmail map[string]domain.User
	nextID  int64
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byID: map[int64]domain.User{}, byEmail: map[string]domain.User{}}
}

func (f *fakeUserRepo) Create(_ context.Context, u domain.User) (domain.User, error) {
	if _, ok := f.byEmail[u.Email]; ok {
		return domain.User{}, domain.ErrEmailTaken
	}
	f.nextID++
	u.ID = f.nextID
	u.CreatedAt = time.Now()
	f.byID[u.ID] = u
	f.byEmail[u.Email] = u
	return u, nil
}
func (f *fakeUserRepo) ByEmail(_ context.Context, email string) (domain.User, error) {
	u, ok := f.byEmail[email]
	if !ok {
		return domain.User{}, domain.ErrNotFound{Entity: "user"}
	}
	return u, nil
}
func (f *fakeUserRepo) ByID(_ context.Context, id int64) (domain.User, error) {
	u, ok := f.byID[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound{Entity: "user", ID: id}
	}
	return u, nil
}
func (f *fakeUserRepo) UpdatePasswordHash(_ context.Context, userID int64, hash string) error {
	u, ok := f.byID[userID]
	if !ok {
		return domain.ErrNotFound{Entity: "user", ID: userID}
	}
	u.PasswordHash = hash
	f.byID[userID] = u
	f.byEmail[u.Email] = u
	return nil
}
func (f *fakeUserRepo) MarkEmailVerified(_ context.Context, userID int64, at time.Time) error {
	u, ok := f.byID[userID]
	if !ok {
		return domain.ErrNotFound{Entity: "user", ID: userID}
	}
	u.EmailVerifiedAt = at
	f.byID[userID] = u
	f.byEmail[u.Email] = u
	return nil
}
func (f *fakeUserRepo) CountAdmins(_ context.Context) (int, error) {
	n := 0
	for _, u := range f.byID {
		if u.Role == domain.RoleAdmin {
			n++
		}
	}
	return n, nil
}

type fakeSessionRepo struct {
	byToken map[string]domain.Session
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{byToken: map[string]domain.Session{}}
}
func (f *fakeSessionRepo) Create(_ context.Context, s domain.Session) error {
	f.byToken[s.Token] = s
	return nil
}
func (f *fakeSessionRepo) ByToken(_ context.Context, token string) (domain.Session, error) {
	s, ok := f.byToken[token]
	if !ok {
		return domain.Session{}, domain.ErrNotFound{Entity: "session"}
	}
	return s, nil
}
func (f *fakeSessionRepo) Delete(_ context.Context, token string) error {
	if _, ok := f.byToken[token]; !ok {
		return domain.ErrNotFound{Entity: "session"}
	}
	delete(f.byToken, token)
	return nil
}
func (f *fakeSessionRepo) DeleteByUser(_ context.Context, userID int64) error {
	for t, s := range f.byToken {
		if s.UserID == userID {
			delete(f.byToken, t)
		}
	}
	return nil
}
func (f *fakeSessionRepo) DeleteExpired(_ context.Context, now time.Time) error {
	for t, s := range f.byToken {
		if s.Expired(now) {
			delete(f.byToken, t)
		}
	}
	return nil
}

type fakeResetRepo struct {
	byToken map[string]domain.PasswordReset
}

func newFakeResetRepo() *fakeResetRepo {
	return &fakeResetRepo{byToken: map[string]domain.PasswordReset{}}
}
func (f *fakeResetRepo) Create(_ context.Context, p domain.PasswordReset) error {
	f.byToken[p.Token] = p
	return nil
}
func (f *fakeResetRepo) ByToken(_ context.Context, token string) (domain.PasswordReset, error) {
	p, ok := f.byToken[token]
	if !ok {
		return domain.PasswordReset{}, domain.ErrNotFound{Entity: "reset"}
	}
	return p, nil
}
func (f *fakeResetRepo) MarkUsed(_ context.Context, token string, usedAt time.Time) error {
	p, ok := f.byToken[token]
	if !ok {
		return domain.ErrNotFound{Entity: "reset"}
	}
	p.UsedAt = usedAt
	f.byToken[token] = p
	return nil
}
func (f *fakeResetRepo) DeleteByUser(_ context.Context, userID int64) error {
	for t, p := range f.byToken {
		if p.UserID == userID {
			delete(f.byToken, t)
		}
	}
	return nil
}

type fakeVerifyRepo struct {
	byToken map[string]domain.EmailVerification
}

func newFakeVerifyRepo() *fakeVerifyRepo {
	return &fakeVerifyRepo{byToken: map[string]domain.EmailVerification{}}
}
func (f *fakeVerifyRepo) Create(_ context.Context, v domain.EmailVerification) error {
	f.byToken[v.Token] = v
	return nil
}
func (f *fakeVerifyRepo) ByToken(_ context.Context, token string) (domain.EmailVerification, error) {
	v, ok := f.byToken[token]
	if !ok {
		return domain.EmailVerification{}, domain.ErrNotFound{Entity: "email verification"}
	}
	return v, nil
}
func (f *fakeVerifyRepo) MarkUsed(_ context.Context, token string, usedAt time.Time) error {
	v, ok := f.byToken[token]
	if !ok {
		return domain.ErrNotFound{Entity: "email verification"}
	}
	v.UsedAt = usedAt
	f.byToken[token] = v
	return nil
}
func (f *fakeVerifyRepo) DeleteByUser(_ context.Context, userID int64) error {
	for t, v := range f.byToken {
		if v.UserID == userID {
			delete(f.byToken, t)
		}
	}
	return nil
}

// fakeRateLimiter: учитывает вызовы по ключам и умеет денаить либо всё (deny), либо
// точечно отдельные ключи (denyKeys) — чтобы тесты различали окна/ключи лимитов.
type fakeRateLimiter struct {
	deny     bool            // денаить любой ключ (грубый общий запрет)
	denyKeys map[string]bool // денаить конкретные ключи (точечно)
	calls    int             // всего вызовов (обратная совместимость)
	byKey    map[string]int  // вызовов на каждый ключ
}

func (f *fakeRateLimiter) Allow(_ context.Context, key string, _ int, _ time.Duration, _ time.Time) (bool, time.Duration, error) {
	f.calls++
	if f.byKey == nil {
		f.byKey = map[string]int{}
	}
	f.byKey[key]++
	if f.deny || f.denyKeys[key] {
		return false, 5 * time.Minute, nil
	}
	return true, 0, nil
}

// fakeHasher: тривиальный, без цены bcrypt. Считает вызовы Compare — чтобы проверить
// тайминг-эквалайзер (Compare прогоняется и для несуществующего пользователя).
type fakeHasher struct {
	compareCalls int
}

func (*fakeHasher) Hash(plain string) (string, error) { return "hash:" + plain, nil }
func (f *fakeHasher) Compare(hash, plain string) error {
	f.compareCalls++
	if hash == "hash:"+plain {
		return nil
	}
	return domain.ErrInvalidCredentials
}

// fakeMailer копит отправленные письма.
type fakeMailer struct {
	sent []Email
}

func (f *fakeMailer) Send(_ context.Context, m Email) error {
	f.sent = append(f.sent, m)
	return nil
}

// fakeSettings — провайдер настроек, отдаёт значения по ключам.
type fakeSettings struct{ flags map[string]bool }

func (f fakeSettings) Bool(_ context.Context, key string) (bool, error) { return f.flags[key], nil }

// authFakes — собранные фейки для доступа из тестов.
type authFakes struct {
	users    *fakeUserRepo
	sessions *fakeSessionRepo
	resets   *fakeResetRepo
	verifies *fakeVerifyRepo
	rl       *fakeRateLimiter
	mailer   *fakeMailer
	hasher   *fakeHasher
}

// buildAuthFull собирает AuthService с фейками и произвольными настройками.
func buildAuthFull(flags map[string]bool) (*AuthService, authFakes) {
	return buildAuthFullWithSetup(flags, "")
}

// buildAuthFullWithSetup как buildAuthFull, но задаёт код первичной настройки
// (SetupToken). Пустой токен → /setup недоступен.
func buildAuthFullWithSetup(flags map[string]bool, setupToken string) (*AuthService, authFakes) {
	f := authFakes{
		users:    newFakeUserRepo(),
		sessions: newFakeSessionRepo(),
		resets:   newFakeResetRepo(),
		verifies: newFakeVerifyRepo(),
		rl:       &fakeRateLimiter{},
		mailer:   &fakeMailer{},
		hasher:   &fakeHasher{},
	}
	svc := NewAuthService(f.users, f.sessions, f.resets, f.verifies, f.rl, f.hasher, f.mailer, fakeSettings{flags: flags}, AuthConfig{
		SessionTTL:      time.Hour,
		ResetTTL:        time.Hour,
		VerifyTTL:       24 * time.Hour,
		AppBaseURL:      "https://app.example",
		LoginRateLimit:  RateRule{Limit: 5, Window: 15 * time.Minute},
		ForgotRateLimit: RateRule{Limit: 3, Window: time.Hour},
		ResendShortRate: RateRule{Limit: 1, Window: time.Minute},
		ResendHourRate:  RateRule{Limit: 5, Window: time.Hour},
		SetupToken:      setupToken,
	})
	return svc, f
}

// buildAuth собирает AuthService с фейками. allowSignup управляет регистрацией.
// Совместимая обёртка для существующих тестов (без подтверждения почты).
func buildAuth(allowSignup bool) (*AuthService, *fakeUserRepo, *fakeSessionRepo, *fakeResetRepo, *fakeRateLimiter, *fakeMailer) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": allowSignup})
	return svc, f.users, f.sessions, f.resets, f.rl, f.mailer
}

// --- тесты ---

func TestAuth_Register_OK(t *testing.T) {
	svc, users, _, _, _, _ := buildAuth(true)
	u, err := svc.Register(context.Background(), "  USER@Example.com ", "password123")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if u.Email != "user@example.com" {
		t.Errorf("email должен нормализоваться, получено %q", u.Email)
	}
	if u.Role != domain.RoleUser {
		t.Errorf("ожидалась роль user, получено %q", u.Role)
	}
	if u.PasswordHash != "hash:password123" {
		t.Errorf("пароль должен быть захеширован, получено %q", u.PasswordHash)
	}
	if _, ok := users.byEmail["user@example.com"]; !ok {
		t.Error("пользователь не сохранён")
	}
}

func TestAuth_Register_SignupClosed(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(false)
	_, err := svc.Register(context.Background(), "a@b.com", "password123")
	if !errors.Is(err, domain.ErrSignupClosed) {
		t.Fatalf("ожидалась ErrSignupClosed, получено %v", err)
	}
}

func TestAuth_Register_Validation(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	_, err := svc.Register(context.Background(), "a@b.com", "short")
	var ve domain.ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("ожидалась ErrValidation для короткого пароля, получено %v", err)
	}
}

func TestAuth_Register_EmailTaken(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Register(ctx, "a@b.com", "password123")
	if !errors.Is(err, domain.ErrEmailTaken) {
		t.Fatalf("ожидалась ErrEmailTaken, получено %v", err)
	}
}

func TestAuth_Login_OK(t *testing.T) {
	svc, _, sess, _, _, _ := buildAuth(true)
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	s, err := svc.Login(ctx, "a@b.com", "password123", "a@b.com|1.2.3.4")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if s.Token == "" {
		t.Error("ожидался непустой токен сессии")
	}
	if _, ok := sess.byToken[s.Token]; !ok {
		t.Error("сессия не сохранена")
	}
}

func TestAuth_Login_WrongPassword(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")
	_, err := svc.Login(ctx, "a@b.com", "wrongpass1", "k")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("ожидалась ErrInvalidCredentials, получено %v", err)
	}
}

func TestAuth_Login_UnknownUser(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	_, err := svc.Login(context.Background(), "nobody@b.com", "password123", "k")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("для несуществующего пользователя ожидалась ErrInvalidCredentials (анти-enum), получено %v", err)
	}
}

func TestAuth_Login_RateLimited(t *testing.T) {
	svc, _, _, _, rl, _ := buildAuth(true)
	rl.deny = true
	_, err := svc.Login(context.Background(), "a@b.com", "password123", "k")
	var re domain.ErrRateLimited
	if !errors.As(err, &re) {
		t.Fatalf("ожидалась ErrRateLimited, получено %v", err)
	}
	if re.RetryAfter <= 0 {
		t.Error("ожидался положительный RetryAfter")
	}
}

func TestAuth_Authenticate_OK(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")
	s, _ := svc.Login(ctx, "a@b.com", "password123", "k")

	u, err := svc.Authenticate(ctx, s.Token)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if u.Email != "a@b.com" {
		t.Errorf("ожидался a@b.com, получено %q", u.Email)
	}
}

func TestAuth_Authenticate_Expired(t *testing.T) {
	svc, _, sess, _, _, _ := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")
	// вручную кладём истёкшую сессию
	expired := domain.Session{Token: "expired-tok", UserID: 1, CreatedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour)}
	_ = sess.Create(ctx, expired)

	_, err := svc.Authenticate(ctx, "expired-tok")
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("ожидалась ErrUnauthenticated для истёкшей сессии, получено %v", err)
	}
	if _, ok := sess.byToken["expired-tok"]; ok {
		t.Error("истёкшая сессия должна быть удалена")
	}
}

func TestAuth_Authenticate_Unknown(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	_, err := svc.Authenticate(context.Background(), "nope")
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("ожидалась ErrUnauthenticated, получено %v", err)
	}
}

func TestAuth_Logout_Idempotent(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	if err := svc.Logout(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("логаут несуществующей сессии должен быть no-op, получено %v", err)
	}
}

func TestAuth_RequestReset_SendsMail(t *testing.T) {
	svc, _, _, resets, _, mailer := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")

	if err := svc.RequestReset(ctx, "a@b.com", "k"); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("ожидалось 1 письмо, отправлено %d", len(mailer.sent))
	}
	if !strings.Contains(mailer.sent[0].Text, "https://app.example/reset?token=") {
		t.Errorf("письмо должно содержать ссылку сброса, текст: %q", mailer.sent[0].Text)
	}
	if len(resets.byToken) != 1 {
		t.Errorf("ожидался 1 токен сброса, есть %d", len(resets.byToken))
	}
}

func TestAuth_RequestReset_UnknownEmail_NoLeak(t *testing.T) {
	svc, _, _, _, rl, mailer := buildAuth(true)
	err := svc.RequestReset(context.Background(), "nobody@b.com", "k")
	if err != nil {
		t.Fatalf("для неизвестного email ожидался nil (анти-enum), получено %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Error("письмо не должно уходить для несуществующего email")
	}
	if rl.calls == 0 {
		t.Error("рейт-лимит должен учитываться даже для несуществующего email")
	}
}

func TestAuth_RequestReset_RateLimited(t *testing.T) {
	svc, _, _, _, rl, mailer := buildAuth(true)
	rl.deny = true
	err := svc.RequestReset(context.Background(), "a@b.com", "k")
	var re domain.ErrRateLimited
	if !errors.As(err, &re) {
		t.Fatalf("ожидалась ErrRateLimited, получено %v", err)
	}
	if len(mailer.sent) != 0 {
		t.Error("при рейт-лимите письмо не должно уходить")
	}
}

func TestAuth_ConfirmReset_OK(t *testing.T) {
	svc, _, sess, resets, _, _ := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")
	s, _ := svc.Login(ctx, "a@b.com", "password123", "k")
	_ = svc.RequestReset(ctx, "a@b.com", "k")
	var token string
	for tk := range resets.byToken {
		token = tk
	}

	if err := svc.ConfirmReset(ctx, token, "newpassword1"); err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	// старая сессия должна быть удалена
	if _, ok := sess.byToken[s.Token]; ok {
		t.Error("после сброса пароля старые сессии должны быть удалены")
	}
	// новый пароль работает
	if _, err := svc.Login(ctx, "a@b.com", "newpassword1", "k"); err != nil {
		t.Errorf("вход с новым паролем должен работать, получено %v", err)
	}
	// старый пароль больше не работает
	if _, err := svc.Login(ctx, "a@b.com", "password123", "k"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("старый пароль не должен работать, получено %v", err)
	}
}

func TestAuth_ConfirmReset_BadToken(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	err := svc.ConfirmReset(context.Background(), "garbage", "newpassword1")
	if !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("ожидалась ErrInvalidToken, получено %v", err)
	}
}

func TestAuth_ConfirmReset_WeakPassword(t *testing.T) {
	svc, _, _, _, _, _ := buildAuth(true)
	err := svc.ConfirmReset(context.Background(), "any", "short")
	var ve domain.ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("ожидалась ErrValidation для слабого пароля, получено %v", err)
	}
}

// --- подтверждение почты ---

func TestAuth_Register_VerifyOff_NoMail(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true}) // verify выключено
	if _, err := svc.Register(context.Background(), "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	if len(f.mailer.sent) != 0 {
		t.Error("без require_email_verification письмо подтверждения не должно уходить")
	}
}

func TestAuth_Register_VerifyOn_SendsMail(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	if _, err := svc.Register(context.Background(), "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	if len(f.mailer.sent) != 1 {
		t.Fatalf("ожидалось 1 письмо подтверждения, отправлено %d", len(f.mailer.sent))
	}
	if !strings.Contains(f.mailer.sent[0].Text, "https://app.example/verify-email?token=") {
		t.Errorf("письмо должно содержать ссылку подтверждения, текст: %q", f.mailer.sent[0].Text)
	}
	if len(f.verifies.byToken) != 1 {
		t.Errorf("ожидался 1 токен подтверждения, есть %d", len(f.verifies.byToken))
	}
}

func TestAuth_Login_VerifyOn_BlockedUntilConfirmed(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	// вход с верным паролем, но почта не подтверждена → ErrEmailNotVerified
	_, err := svc.Login(ctx, "a@b.com", "password123", "k")
	if !errors.Is(err, domain.ErrEmailNotVerified) {
		t.Fatalf("ожидалась ErrEmailNotVerified, получено %v", err)
	}

	// подтверждаем по токену
	var token string
	for tk := range f.verifies.byToken {
		token = tk
	}
	if err := svc.ConfirmEmailVerification(ctx, token); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	// теперь вход проходит
	if _, err := svc.Login(ctx, "a@b.com", "password123", "k"); err != nil {
		t.Errorf("после подтверждения вход должен работать, получено %v", err)
	}
}

func TestAuth_ConfirmEmailVerification_BadToken(t *testing.T) {
	svc, _ := buildAuthFull(map[string]bool{})
	if err := svc.ConfirmEmailVerification(context.Background(), "garbage"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("ожидалась ErrInvalidToken, получено %v", err)
	}
}

func TestAuth_ResendVerification_RateLimited(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"require_email_verification": true})
	f.rl.deny = true
	err := svc.ResendVerification(context.Background(), "a@b.com", "k")
	var re domain.ErrRateLimited
	if !errors.As(err, &re) {
		t.Fatalf("ожидалась ErrRateLimited, получено %v", err)
	}
}

func TestAuth_ResendVerification_UnknownEmail_NoLeak(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"require_email_verification": true})
	if err := svc.ResendVerification(context.Background(), "nobody@b.com", "k"); err != nil {
		t.Fatalf("для неизвестного email ожидался nil, получено %v", err)
	}
	if len(f.mailer.sent) != 0 {
		t.Error("письмо не должно уходить для несуществующего email")
	}
}

// --- дополнительное покрытие (по аудиту) ---

// ConfirmReset: токен одноразовый — повторное использование отвергается.
func TestAuth_ConfirmReset_TokenReuse(t *testing.T) {
	svc, _, _, resets, _, _ := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")
	_ = svc.RequestReset(ctx, "a@b.com", "k")
	var token string
	for tk := range resets.byToken {
		token = tk
	}
	if err := svc.ConfirmReset(ctx, token, "newpassword1"); err != nil {
		t.Fatalf("первый сброс должен пройти: %v", err)
	}
	// повторное использование того же токена → ErrInvalidToken
	if err := svc.ConfirmReset(ctx, token, "anotherpass1"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("повторное использование токена должно дать ErrInvalidToken, получено %v", err)
	}
}

// ConfirmReset: истёкший токен отвергается.
func TestAuth_ConfirmReset_Expired(t *testing.T) {
	svc, users, _, resets, _, _ := buildAuth(true)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "a@b.com", "password123")
	_ = users
	// вручную кладём истёкший токен сброса
	_ = resets.Create(ctx, domain.PasswordReset{
		Token: "expired-reset", UserID: u.ID,
		CreatedAt: time.Now().Add(-2 * time.Hour), ExpiresAt: time.Now().Add(-time.Hour),
	})
	if err := svc.ConfirmReset(ctx, "expired-reset", "newpassword1"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("истёкший токен должен дать ErrInvalidToken, получено %v", err)
	}
}

// ConfirmEmailVerification: успешный путь подтверждает почту, повтор токена отвергается.
func TestAuth_ConfirmEmailVerification_OK_AndReuse(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	var token string
	for tk := range f.verifies.byToken {
		token = tk
	}
	if err := svc.ConfirmEmailVerification(ctx, token); err != nil {
		t.Fatalf("подтверждение должно пройти: %v", err)
	}
	// пользователь стал подтверждённым
	u, _ := f.users.ByEmail(ctx, "a@b.com")
	if !u.EmailVerified() {
		t.Error("после подтверждения EmailVerified должен быть true")
	}
	// повтор того же токена → ErrInvalidToken
	if err := svc.ConfirmEmailVerification(ctx, token); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("повтор токена подтверждения должен дать ErrInvalidToken, получено %v", err)
	}
}

// Login: при включённом подтверждении и НЕВЕРНОМ пароле отдаётся ErrInvalidCredentials,
// а не ErrEmailNotVerified — проверка почты идёт строго ПОСЛЕ пароля (не раскрывает
// статус аккаунта тому, кто пароль не знает).
func TestAuth_Login_VerifyOn_WrongPassword_NotLeakVerifyStatus(t *testing.T) {
	svc, _ := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Login(ctx, "a@b.com", "wrongpass1", "k")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("при неверном пароле ожидалась ErrInvalidCredentials (не EmailNotVerified), получено %v", err)
	}
}

// ResendVerification: для существующего неподтверждённого — письмо уходит, токен создан.
func TestAuth_ResendVerification_SendsMail(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	before := len(f.mailer.sent)
	if err := svc.ResendVerification(ctx, "a@b.com", "k"); err != nil {
		t.Fatalf("resend: %v", err)
	}
	if len(f.mailer.sent) != before+1 {
		t.Errorf("ожидалось +1 письмо подтверждения, было %d стало %d", before, len(f.mailer.sent))
	}
}

// ResendVerification: уже подтверждённому пользователю письмо не уходит (но ответ
// нейтральный — nil).
func TestAuth_ResendVerification_AlreadyVerified_NoMail(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	ctx := context.Background()
	u, _ := svc.Register(ctx, "a@b.com", "password123")
	_ = f.users.MarkEmailVerified(ctx, u.ID, time.Now())
	sentBefore := len(f.mailer.sent)
	if err := svc.ResendVerification(ctx, "a@b.com", "k"); err != nil {
		t.Fatalf("resend для подтверждённого должен быть nil, получено %v", err)
	}
	if len(f.mailer.sent) != sentBefore {
		t.Error("для уже подтверждённого письмо не должно уходить")
	}
}

// randomToken: токены уникальны и достаточной длины (через выпуск нескольких сессий).
func TestAuth_SessionTokens_UniqueAndLong(t *testing.T) {
	svc, _, sess, _, _, _ := buildAuth(true)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@b.com", "password123")
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		s, err := svc.Login(ctx, "a@b.com", "password123", "k")
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		if len(s.Token) < 32 {
			t.Errorf("токен сессии слишком короткий: %d символов", len(s.Token))
		}
		if seen[s.Token] {
			t.Fatal("токены сессий должны быть уникальны")
		}
		seen[s.Token] = true
	}
	if len(sess.byToken) != 20 {
		t.Errorf("ожидалось 20 уникальных сессий, есть %d", len(sess.byToken))
	}
}

// --- первичная настройка (/setup) ---

func TestAuth_Setup_CreatesFirstAdmin(t *testing.T) {
	svc, f := buildAuthFullWithSetup(map[string]bool{}, "secret-code")
	ctx := context.Background()
	needed, err := svc.SetupNeeded(ctx)
	if err != nil || !needed {
		t.Fatalf("ожидалась доступная настройка: needed=%v err=%v", needed, err)
	}
	if err := svc.Setup(ctx, "admin@b.com", "password123", "secret-code"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	u, ok := f.users.byEmail["admin@b.com"]
	if !ok || u.Role != domain.RoleAdmin {
		t.Fatalf("ожидался созданный админ, получено %+v ok=%v", u, ok)
	}
}

func TestAuth_Setup_WrongToken(t *testing.T) {
	svc, _ := buildAuthFullWithSetup(map[string]bool{}, "secret-code")
	ctx := context.Background()
	if err := svc.Setup(ctx, "admin@b.com", "password123", "wrong-token"); !errors.Is(err, domain.ErrSetupToken) {
		t.Fatalf("ожидалась ErrSetupToken, получено %v", err)
	}
}

func TestAuth_Setup_DisabledWithoutToken(t *testing.T) {
	// Пустой SetupToken → /setup недоступен (даже без админа).
	svc, _ := buildAuthFullWithSetup(map[string]bool{}, "")
	ctx := context.Background()
	needed, err := svc.SetupNeeded(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if needed {
		t.Error("без SetupToken первичная настройка должна быть недоступна")
	}
	if err := svc.Setup(ctx, "admin@b.com", "password123", ""); !errors.Is(err, domain.ErrSetupClosed) {
		t.Fatalf("ожидалась ErrSetupClosed, получено %v", err)
	}
}

func TestAuth_Setup_ClosedAfterFirstAdmin(t *testing.T) {
	svc, _ := buildAuthFullWithSetup(map[string]bool{}, "secret-code")
	ctx := context.Background()
	if err := svc.Setup(ctx, "admin@b.com", "password123", "secret-code"); err != nil {
		t.Fatal(err)
	}
	// повторный вызов тем же токеном → закрыто
	if err := svc.Setup(ctx, "other@b.com", "password123", "secret-code"); !errors.Is(err, domain.ErrSetupClosed) {
		t.Fatalf("повторный setup должен быть закрыт (ErrSetupClosed), получено %v", err)
	}
}

func TestAuth_SetupNeeded_FalseWhenAdminExists(t *testing.T) {
	svc, _ := buildAuthFullWithSetup(map[string]bool{}, "secret-code")
	ctx := context.Background()
	// сначала заведём админа через первичную настройку
	if err := svc.Setup(ctx, "admin@b.com", "password123", "secret-code"); err != nil {
		t.Fatal(err)
	}
	needed, err := svc.SetupNeeded(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if needed {
		t.Error("при существующем админе setup не нужен")
	}
	// и /setup закрыт
	if err := svc.Setup(ctx, "x@b.com", "password123", "secret-code"); !errors.Is(err, domain.ErrSetupClosed) {
		t.Fatalf("ожидалась ErrSetupClosed, получено %v", err)
	}
}

// --- security-инварианты (по аудиту тестов) ---

// Logout должен инвалидировать сессию на СЕРВЕРЕ, а не только погасить cookie:
// после Logout(token) Authenticate(token) обязан вернуть ErrUnauthenticated.
func TestAuth_Logout_InvalidatesSession(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	s, err := svc.Login(ctx, "a@b.com", "password123", "k")
	if err != nil {
		t.Fatal(err)
	}
	// сессия живёт до логаута
	if _, err := svc.Authenticate(ctx, s.Token); err != nil {
		t.Fatalf("до логаута сессия должна быть валидна, получено %v", err)
	}
	if err := svc.Logout(ctx, s.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	// после логаута — недействительна на сервере
	if _, err := svc.Authenticate(ctx, s.Token); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("после логаута Authenticate должен дать ErrUnauthenticated, получено %v", err)
	}
	if _, ok := f.sessions.byToken[s.Token]; ok {
		t.Error("сессия должна быть удалена из хранилища")
	}
}

// Повторный RequestReset должен инвалидировать ПРЕЖНИЙ токен сброса (иначе утёкший
// старый токен оставался бы живым). Первый токен после второго запроса нерабочий.
func TestAuth_RequestReset_InvalidatesOldToken(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestReset(ctx, "a@b.com", "k"); err != nil {
		t.Fatal(err)
	}
	first := onlyResetToken(t, f)

	if err := svc.RequestReset(ctx, "a@b.com", "k"); err != nil {
		t.Fatal(err)
	}
	// после второго запроса прежний токен не должен работать
	if err := svc.ConfirmReset(ctx, first, "newpassword1"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("старый reset-токен должен быть инвалидирован, получено %v", err)
	}
	// а ровно один новый токен — рабочий
	if len(f.resets.byToken) != 1 {
		t.Errorf("ожидался ровно 1 активный токен сброса, есть %d", len(f.resets.byToken))
	}
}

// ConfirmReset должен снести ВСЕ сессии пользователя (а не одну) — сброс пароля
// разлогинивает на всех устройствах.
func TestAuth_ConfirmReset_KillsAllSessions(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	// три устройства = три сессии
	for i := 0; i < 3; i++ {
		if _, err := svc.Login(ctx, "a@b.com", "password123", "k"); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.sessions.byToken) != 3 {
		t.Fatalf("ожидалось 3 сессии, есть %d", len(f.sessions.byToken))
	}
	if err := svc.RequestReset(ctx, "a@b.com", "k"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmReset(ctx, onlyResetToken(t, f), "newpassword1"); err != nil {
		t.Fatal(err)
	}
	if len(f.sessions.byToken) != 0 {
		t.Errorf("после сброса пароля все сессии должны быть сняты, осталось %d", len(f.sessions.byToken))
	}
}

// Анти-enumeration по ТАЙМИНГУ: для несуществующего пользователя Login всё равно
// прогоняет Compare (против заглушки), чтобы по времени ответа нельзя было отличить
// «нет email» от «неверный пароль». Закрепляем сам факт вызова Compare.
func TestAuth_Login_UnknownUser_RunsCompare(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true})
	ctx := context.Background()
	_, err := svc.Login(ctx, "nobody@b.com", "password123", "k")
	if !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("ожидалась ErrInvalidCredentials, получено %v", err)
	}
	if f.hasher.compareCalls != 1 {
		t.Errorf("для несуществующего пользователя Compare должен вызываться ровно 1 раз "+
			"(тайминг-эквалайзер), вызвано %d", f.hasher.compareCalls)
	}
}

// login и forgot должны использовать РАЗНЫЕ ключи рейт-лимита — исчерпание одного не
// должно блокировать другой по тому же rateKey.
func TestAuth_RateLimit_LoginAndForgot_DistinctKeys(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	const rateKey = "a@b.com|1.2.3.4"
	if _, err := svc.Login(ctx, "a@b.com", "password123", rateKey); err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestReset(ctx, "a@b.com", rateKey); err != nil {
		t.Fatal(err)
	}
	// ключи различны → у каждого ровно один вызов
	if got := f.rl.byKey["login:"+rateKey]; got != 1 {
		t.Errorf("login должен учитываться по ключу login:%s (вызовов %d)", rateKey, got)
	}
	if got := f.rl.byKey["forgot:"+rateKey]; got != 1 {
		t.Errorf("forgot должен учитываться по ключу forgot:%s (вызовов %d)", rateKey, got)
	}
}

// Resend подтверждения почты ограничен ДВУМЯ окнами: короткое (пауза между письмами)
// и часовой потолок. Это разные ключи лимита — проверяем, что оба проверяются и что
// срабатывание часового окна (при разрешённом коротком) блокирует отправку.
func TestAuth_ResendVerification_TwoWindows(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"require_email_verification": true})
	ctx := context.Background()
	const rateKey = "a@b.com|ip"
	// оба окна проверяются (разные ключи)
	if err := svc.ResendVerification(ctx, "a@b.com", rateKey); err != nil {
		t.Fatal(err)
	}
	if f.rl.byKey["resend-short:"+rateKey] != 1 || f.rl.byKey["resend-hour:"+rateKey] != 1 {
		t.Fatalf("должны проверяться ОБА окна resend, получено short=%d hour=%d",
			f.rl.byKey["resend-short:"+rateKey], f.rl.byKey["resend-hour:"+rateKey])
	}
	// часовой потолок исчерпан, короткое окно свободно → отправка всё равно блокируется
	f.rl.denyKeys = map[string]bool{"resend-hour:" + rateKey: true}
	err := svc.ResendVerification(ctx, "a@b.com", rateKey)
	var re domain.ErrRateLimited
	if !errors.As(err, &re) {
		t.Fatalf("при исчерпанном часовом окне ожидалась ErrRateLimited, получено %v", err)
	}
}

// Истёкший verify-токен не подтверждает почту (по аналогии с reset).
func TestAuth_ConfirmEmailVerification_Expired(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true})
	ctx := context.Background()
	u, err := svc.Register(ctx, "a@b.com", "password123")
	if err != nil {
		t.Fatal(err)
	}
	// кладём заведомо истёкший токен
	expired := domain.EmailVerification{
		Token:     "verify-expired",
		UserID:    u.ID,
		CreatedAt: time.Now().Add(-48 * time.Hour),
		ExpiresAt: time.Now().Add(-24 * time.Hour),
	}
	if err := f.verifies.Create(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmEmailVerification(ctx, "verify-expired"); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("истёкший verify-токен должен дать ErrInvalidToken, получено %v", err)
	}
}

// Повторная отправка письма подтверждения инвалидирует ПРЕЖНИЙ verify-токен
// (issueAndSendVerification удаляет старые токены пользователя перед выпуском нового).
func TestAuth_ResendVerification_InvalidatesOldToken(t *testing.T) {
	svc, f := buildAuthFull(map[string]bool{"allow_signup": true, "require_email_verification": true})
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@b.com", "password123"); err != nil {
		t.Fatal(err)
	}
	first := onlyVerifyToken(t, f)
	if err := svc.ResendVerification(ctx, "a@b.com", "k"); err != nil {
		t.Fatal(err)
	}
	// прежний токен не должен подтверждать почту
	if err := svc.ConfirmEmailVerification(ctx, first); !errors.Is(err, domain.ErrInvalidToken) {
		t.Fatalf("старый verify-токен должен быть инвалидирован, получено %v", err)
	}
	if len(f.verifies.byToken) != 1 {
		t.Errorf("ожидался ровно 1 активный verify-токен, есть %d", len(f.verifies.byToken))
	}
}

// onlyResetToken возвращает единственный токен сброса (фейл, если их не ровно один).
func onlyResetToken(t *testing.T, f authFakes) string {
	t.Helper()
	if len(f.resets.byToken) != 1 {
		t.Fatalf("ожидался ровно 1 токен сброса, есть %d", len(f.resets.byToken))
	}
	for tk := range f.resets.byToken {
		return tk
	}
	return ""
}

// onlyVerifyToken возвращает единственный verify-токен (фейл, если их не ровно один).
func onlyVerifyToken(t *testing.T, f authFakes) string {
	t.Helper()
	if len(f.verifies.byToken) != 1 {
		t.Fatalf("ожидался ровно 1 verify-токен, есть %d", len(f.verifies.byToken))
	}
	for tk := range f.verifies.byToken {
		return tk
	}
	return ""
}
