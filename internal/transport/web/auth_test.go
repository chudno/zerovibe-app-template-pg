// E2E-тесты аутентификации на полном стеке (httptest + локальный PostgreSQL).
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// postForm — POST с form-encoded телом и опциональной cookie.
func postForm(h http.Handler, path string, vals url.Values, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c != nil {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLogin_SetsCookie(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	c := seedAdminAndLogin(t, h, auth, "a@b.com", "password123")
	if c.Value == "" {
		t.Fatal("ожидалась непустая cookie сессии")
	}
	if !c.HttpOnly {
		t.Error("cookie сессии должна быть HttpOnly")
	}
}

// Обычный (не превью) режим: cookie сессии — SameSite=Lax (CSRF-защита), без Secure
// (SecureCookie=false в тестах).
func TestLogin_Cookie_NormalModeIsLax(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	c := seedAdminAndLogin(t, h, auth, "a@b.com", "password123")
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("вне превью cookie должна быть SameSite=Lax, получено %v", c.SameSite)
	}
	if c.Secure {
		t.Error("вне превью при SecureCookie=false cookie не должна быть Secure")
	}
}

// Превью-режим (cross-site iframe): cookie сессии — SameSite=None; Secure, иначе
// браузер не сохранит её во фрейме и вход не удержится.
func TestLogin_Cookie_PreviewModeIsNoneSecure(t *testing.T) {
	h, auth := buildStackPreview(t)
	c := seedAdminAndLogin(t, h, auth, "a@b.com", "password123")
	if c.SameSite != http.SameSiteNoneMode {
		t.Errorf("в превью cookie должна быть SameSite=None, получено %v", c.SameSite)
	}
	if !c.Secure {
		t.Error("в превью cookie должна быть Secure (иначе браузер отвергнет SameSite=None)")
	}
	if !c.HttpOnly {
		t.Error("cookie сессии должна оставаться HttpOnly и в превью")
	}
}

// Неверный пароль на форме входа: страница перерисовывается с ошибкой (200, НЕ 401 —
// форма того же origin показывает текст ошибки), сессионная cookie не ставится.
func TestLogin_WrongPassword_RerendersFormNoCookie(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	if err := auth.Setup(context.Background(), "a@b.com", "password123", testSetupToken); err != nil {
		t.Fatal(err)
	}
	rec := postForm(h, "/login", url.Values{"email": {"a@b.com"}, "password": {"wrongpass1"}}, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("форма входа с ошибкой перерисовывается со статусом 200, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "неверный email или пароль") {
		t.Errorf("ожидался текст ошибки на перерисованной форме, тело: %s", rec.Body.String())
	}
	// cookie сессии не ставится
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "zv_session" && ck.Value != "" {
			t.Error("при неверном пароле cookie сессии не должна ставиться")
		}
	}
}

func TestProtectedRedirectsWhenAnonymous(t *testing.T) {
	h, _, _ := buildStack(t, false)
	// Защищённый раздел (/admin/settings) — гостя уводит на /login.
	req := httptest.NewRequest("GET", "/admin/settings", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("гость на /admin/settings должен получить редирект 303, получен %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("ожидался редирект на /login, получено %q", loc)
	}
}

func TestLandingPublicForAnonymous(t *testing.T) {
	h, _, _ := buildStack(t, false)
	// Главная / — публичный лендинг, гость видит 200 (не редирект).
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("гость на / (лендинг) должен получить 200, получен %d", rec.Code)
	}
	body := rec.Body.String()
	// Главная — название приложения по центру (define "landing"/"app-logo").
	if !strings.Contains(body, "app-logo") && !strings.Contains(body, `class="landing"`) {
		t.Error("главная должна показывать название приложения по центру")
	}
	// Техничка (стек) НЕ должна протекать конечному пользователю — и бренд
	// платформы тоже: приложение — продукт ПОЛЬЗОВАТЕЛЯ, слово Zerovibe в нём
	// недопустимо (жалобы тестеров 28 июля: логотип платформы в чужих проектах).
	for _, leak := range []string{"DaisyUI", "HTMX", "PostgreSQL", "SQLite", "Эталонный шаблон", "Zerovibe", "zerovibe_", "vibe_"} {
		if strings.Contains(body, leak) {
			t.Errorf("лендинг не должен содержать техничку: %q", leak)
		}
	}
}

func TestProtectedAccessibleWithCookie(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	// seedAdminAndLogin даёт cookie админа — единственная роль, которой открыт
	// защищённый /admin/settings.
	c := seedAdminAndLogin(t, h, auth, "a@b.com", "password123")
	req := httptest.NewRequest("GET", "/admin/settings", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("с cookie админа /admin/settings должен открываться, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Error("ожидалась полная страница")
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	c := seedAdminAndLogin(t, h, auth, "a@b.com", "password123")
	rec := postForm(h, "/logout", url.Values{}, c)
	var cleared bool
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "zv_session" && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout должен очищать cookie сессии (MaxAge<0)")
	}
}

func TestRegisterClosed_GET_ShowsStub(t *testing.T) {
	h, _, _ := buildStack(t, false)
	req := httptest.NewRequest("GET", "/register", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ожидался 200, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "закрыта") {
		t.Error("при закрытой регистрации страница должна показывать заглушку")
	}
}

func TestRegisterClosed_POST_Rejected(t *testing.T) {
	h, _, _ := buildStack(t, false)
	rec := postForm(h, "/register", url.Values{"email": {"x@y.com"}, "password": {"password123"}}, nil)
	// failForm перерисовывает форму регистрации; но cookie не ставится и юзер не входит
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "zv_session" && ck.Value != "" {
			t.Error("при закрытой регистрации сессия не должна создаваться")
		}
	}
}

func TestRegisterOpen_CreatesAndLogsIn(t *testing.T) {
	h, _, _ := buildStack(t, true) // allowSignup=true
	rec := postForm(h, "/register", url.Values{"email": {"new@user.com"}, "password": {"password123"}}, nil)
	var got *http.Cookie
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == "zv_session" && ck.Value != "" {
			got = ck
		}
	}
	if got == nil {
		t.Fatalf("после открытой регистрации ожидался автологин (cookie), код %d, тело: %s", rec.Code, rec.Body.String())
	}
	// автологин действует: с cookie главная (/) открывается пользователю (200).
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(got)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("новый пользователь должен видеть главную, получен %d", rec2.Code)
	}
}

func TestForgot_UnknownEmail_NeutralNoMail(t *testing.T) {
	h, _, _ := buildStack(t, false)
	rec := postForm(h, "/forgot", url.Values{"email": {"nobody@x.com"}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot должен отдавать нейтральный 200, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Если такой email") {
		t.Error("ожидался нейтральный ответ (анти-enumeration)")
	}
}

func TestReset_ChangesPassword(t *testing.T) {
	// Соберём стек с мейлером-перехватчиком, чтобы достать токен из письма.
	h, auth, _ := buildStackWithMailer(t, false)
	ctx := context.Background()
	if err := auth.Setup(ctx, "a@b.com", "password123", testSetupToken); err != nil {
		t.Fatal(err)
	}
	// запросим сброс через usecase напрямую (транспорт forgot не отдаёт токен)
	if err := auth.RequestReset(ctx, "a@b.com", "k"); err != nil {
		t.Fatal(err)
	}
	token := capturedToken
	if token == "" {
		t.Fatal("токен сброса не перехвачен")
	}

	rec := postForm(h, "/reset", url.Values{"token": {token}, "password": {"newpassword1"}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset должен пройти, получен %d: %s", rec.Code, rec.Body.String())
	}
	// войти новым паролем
	c := loginCookie(t, h, "a@b.com", "newpassword1")
	if c.Value == "" {
		t.Error("вход с новым паролем должен работать")
	}
}

func TestReset_BadToken_400Ish(t *testing.T) {
	h, _, _ := buildStack(t, false)
	rec := postForm(h, "/reset", url.Values{"token": {"garbage"}, "password": {"newpassword1"}}, nil)
	// reset перерисовывает страницу с ошибкой (200 с текстом), не 500
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("плохой токен не должен давать 500, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "недействительна") {
		t.Error("ожидалось сообщение о недействительной ссылке")
	}
}

func TestLoginRateLimited_429(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	if err := auth.Setup(context.Background(), "a@b.com", "password123", testSetupToken); err != nil {
		t.Fatal(err)
	}
	// лимит логина 5/15мин → 6-я неверная попытка по тому же email → 429
	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = postForm(h, "/login", url.Values{"email": {"a@b.com"}, "password": {"wrongpass1"}}, nil)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Errorf("после превышения лимита ожидался заголовок Retry-After, тело: %s", last.Body.String())
	}
}

func TestEmailVerification_BlocksLoginThenConfirms(t *testing.T) {
	h, auth, settings := buildStackWithMailer(t, true) // allowSignup + перехват токенов
	ctx := context.Background()
	if err := settings.Set(ctx, "require_email_verification", "true"); err != nil {
		t.Fatalf("включить подтверждение: %v", err)
	}

	// регистрация: аккаунт создаётся, письмо с токеном уходит (перехватываем)
	recReg := postForm(h, "/register", url.Values{"email": {"v@user.com"}, "password": {"password123"}}, nil)
	// после регистрации с verify вход заблокирован — cookie не должно быть
	for _, ck := range recReg.Result().Cookies() {
		if ck.Name == "zv_session" && ck.Value != "" {
			t.Error("при включённом подтверждении автологин не должен происходить")
		}
	}

	// попытка входа до подтверждения → страница «подтвердите почту»
	recLogin := postForm(h, "/login", url.Values{"email": {"v@user.com"}, "password": {"password123"}}, nil)
	if !strings.Contains(recLogin.Body.String(), "подтвержден") && !strings.Contains(recLogin.Body.String(), "подтверд") {
		t.Errorf("ожидалась страница подтверждения почты, тело: %s", recLogin.Body.String())
	}

	// подтверждаем по перехваченному токену
	if capturedVerifyToken == "" {
		t.Fatal("токен подтверждения не перехвачен из письма")
	}
	recVerify := httptest.NewRequest("GET", "/verify-email?token="+capturedVerifyToken, nil)
	recVerifyRec := httptest.NewRecorder()
	h.ServeHTTP(recVerifyRec, recVerify)
	if recVerifyRec.Code != http.StatusOK {
		t.Fatalf("подтверждение должно пройти, код %d", recVerifyRec.Code)
	}

	// теперь вход проходит и ставит cookie
	c := loginCookie(t, h, "v@user.com", "password123")
	if c.Value == "" {
		t.Error("после подтверждения вход должен работать")
	}
	_ = auth
}

func TestAdminSettings_RequiresAdmin(t *testing.T) {
	h, _, _ := buildStack(t, true)
	// обычный пользователь (через регистрацию) — не админ
	recReg := postForm(h, "/register", url.Values{"email": {"plain@user.com"}, "password": {"password123"}}, nil)
	var c *http.Cookie
	for _, ck := range recReg.Result().Cookies() {
		if ck.Name == "zv_session" && ck.Value != "" {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("пользователь не залогинен")
	}
	req := httptest.NewRequest("GET", "/admin/settings", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("не-админ на /admin/settings должен получить 403, получен %d", rec.Code)
	}
}

func TestSetup_CreatesFirstAdminThenClosed(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	needed, err := auth.SetupNeeded(context.Background())
	if err != nil || !needed {
		t.Fatalf("ожидалась доступная настройка: needed=%v err=%v", needed, err)
	}

	// неверный токен → 403
	bad := postForm(h, "/setup", url.Values{"email": {"a@b.com"}, "password": {"password123"}, "token": {"nope"}}, nil)
	if bad.Code != http.StatusForbidden {
		t.Errorf("неверный токен → ожидался 403, получен %d", bad.Code)
	}

	// верный токен → 201, админ создан
	ok := postForm(h, "/setup", url.Values{"email": {"a@b.com"}, "password": {"password123"}, "token": {testSetupToken}}, nil)
	if ok.Code != http.StatusCreated {
		t.Fatalf("создание админа → ожидался 201, получен %d: %s", ok.Code, ok.Body.String())
	}
	// созданный админ может войти
	if c := loginCookie(t, h, "a@b.com", "password123"); c.Value == "" {
		t.Error("созданный через /setup админ должен входить")
	}

	// повторный /setup → 410 (закрыто)
	again := postForm(h, "/setup", url.Values{"email": {"x@b.com"}, "password": {"password123"}, "token": {testSetupToken}}, nil)
	if again.Code != http.StatusGone {
		t.Errorf("повторный /setup → ожидался 410, получен %d", again.Code)
	}
}

// /setup принимает не только form, но и JSON-тело (агенту после деплоя так удобнее).
func TestSetup_AcceptsJSONBody(t *testing.T) {
	h, _, _ := buildStack(t, false)
	body := strings.NewReader(`{"email":"a@b.com","password":"password123","token":"` + testSetupToken + `"}`)
	req := httptest.NewRequest("POST", "/setup", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("JSON /setup → ожидался 201, получен %d: %s", rec.Code, rec.Body.String())
	}
	// созданный админ реально входит
	if c := loginCookie(t, h, "a@b.com", "password123"); c.Value == "" {
		t.Error("админ, созданный через JSON /setup, должен входить")
	}
}

// --- GET-страницы (публичные) отдаются (закрываем 0%-покрытие страниц) ---

func TestPublicPages_RenderForGuest(t *testing.T) {
	h, _, _ := buildStack(t, true) // allowSignup=true, чтобы /register имел смысл
	for _, path := range []string{"/login", "/register", "/forgot", "/reset?token=abc"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: ожидался 200, получен %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("GET %s: ожидалась полная страница", path)
		}
	}
}

// Залогиненного пользователя страницы входа/регистрации перенаправляют на / (303),
// чтобы он не видел форму входа поверх уже активной сессии.
func TestLoginRegisterPages_RedirectWhenAuthed(t *testing.T) {
	h, auth, _ := buildStack(t, true)
	c := seedAdminAndLogin(t, h, auth, "a@b.com", "password123")
	for _, path := range []string{"/login", "/register"} {
		req := httptest.NewRequest("GET", path, nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("GET %s залогиненным → ожидался 303, получен %d", path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/" {
			t.Errorf("GET %s залогиненным → Location %q, ожидался /", path, loc)
		}
	}
}

// Страница настроек доступна админу и отдаёт известные настройки.
func TestSettingsPage_RendersForAdmin(t *testing.T) {
	h, auth, _ := buildStack(t, false)
	c := seedAdminAndLogin(t, h, auth, "admin@b.com", "password123")
	req := httptest.NewRequest("GET", "/admin/settings", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("админ на /admin/settings → ожидался 200, получен %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "allow_signup") {
		t.Error("страница настроек должна показывать allow_signup")
	}
}

// PUT /admin/settings меняет настройку: после включения allow_signup регистрация
// открывается (проверяем сквозной эффект, а не только код ответа).
func TestSetSetting_AdminTogglesSignup(t *testing.T) {
	h, auth, settings := buildStack(t, false) // регистрация изначально закрыта
	c := seedAdminAndLogin(t, h, auth, "admin@b.com", "password123")

	form := url.Values{"key": {"allow_signup"}, "value": {"true"}}
	req := httptest.NewRequest("PUT", "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT /admin/settings → ожидался 204, получен %d: %s", rec.Code, rec.Body.String())
	}
	// эффект: настройка реально записана
	if on, _ := settings.Bool(context.Background(), "allow_signup"); !on {
		t.Error("после PUT allow_signup должна быть включена")
	}
}

// PUT /admin/settings запрещён гостю (требует роль admin).
func TestSetSetting_RequiresAdmin(t *testing.T) {
	h, _, _ := buildStack(t, false)
	form := url.Values{"key": {"allow_signup"}, "value": {"true"}}
	req := httptest.NewRequest("PUT", "/admin/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// гость без сессии → редирект на вход (303), не применяем настройку
	if rec.Code != http.StatusSeeOther {
		t.Errorf("гость на PUT /admin/settings → ожидался редирект 303, получен %d", rec.Code)
	}
}

// /healthz отвечает 200 без аутентификации (для healthcheck платформы/контейнера).
func TestHealthz_OK(t *testing.T) {
	h, _, _ := buildStack(t, false)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /healthz → ожидался 200, получен %d", rec.Code)
	}
}

// POST /resend-verification всегда отвечает страницей (200) и не раскрывает, есть ли
// такой email (анти-enumeration на уровне транспорта).
func TestResendVerification_NoEnumeration(t *testing.T) {
	h, _, _ := buildStack(t, false)
	rec := postForm(h, "/resend-verification", url.Values{"email": {"nobody@b.com"}}, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("POST /resend-verification → ожидался 200, получен %d", rec.Code)
	}
}
