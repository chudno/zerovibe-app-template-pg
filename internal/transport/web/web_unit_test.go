// Юнит-тесты транспортного слоя без полного стека: маппинг ошибок в HTTP-коды и
// определение IP клиента. Это контракт ответов и security-граница рейт-лимита —
// держим их под тестом, чтобы ветки не регрессировали незаметно.
package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chudno/zerovibe/internal/domain"
)

// newBareServer собирает Server без сервисов — для тестов чистых функций транспорта
// (fail/clientIP), которым бизнес-слой не нужен (нужны только шаблоны и cfg).
func newBareServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(nil, nil, Config{SecureCookie: false, CookieName: "zv_session"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

// TestFail_ErrorToStatusMapping — табличный тест единой точки обработки ошибок fail().
// Каждая доменная ошибка должна давать строго определённый HTTP-статус: это контракт,
// на который опираются и формы, и htmx, и внешние вызовы. Покрывает все ветки switch.
func TestFail_ErrorToStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"not found", domain.ErrNotFound{Entity: "note"}, http.StatusNotFound},
		{"validation", domain.ErrValidation{Msg: "плохой ввод"}, http.StatusBadRequest},
		{"invalid credentials", domain.ErrInvalidCredentials, http.StatusUnauthorized},
		{"signup closed", domain.ErrSignupClosed, http.StatusForbidden},
		{"email taken", domain.ErrEmailTaken, http.StatusConflict},
		{"forbidden", domain.ErrForbidden, http.StatusForbidden},
		{"invalid token", domain.ErrInvalidToken, http.StatusBadRequest},
		{"email not verified", domain.ErrEmailNotVerified, http.StatusForbidden},
		{"setup closed", domain.ErrSetupClosed, http.StatusGone},
		{"setup token", domain.ErrSetupToken, http.StatusForbidden},
		{"rate limited", domain.ErrRateLimited{RetryAfter: 90 * time.Second}, http.StatusTooManyRequests},
		{"unknown → 500", errUnexpected("boom"), http.StatusInternalServerError},
	}
	srv := newBareServer(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/x", nil) // не-htmx
			srv.fail(rec, req, tc.err)
			if rec.Code != tc.wantStatus {
				t.Errorf("ошибка %v → статус %d, ожидался %d", tc.err, rec.Code, tc.wantStatus)
			}
		})
	}
}

// TestFail_RateLimited_SetsRetryAfter — при 429 должен ставиться заголовок Retry-After
// в секундах, чтобы клиент знал, когда повторить.
func TestFail_RateLimited_SetsRetryAfter(t *testing.T) {
	srv := newBareServer(t)
	rec := httptest.NewRecorder()
	srv.fail(rec, httptest.NewRequest("POST", "/x", nil), domain.ErrRateLimited{RetryAfter: 90 * time.Second})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("ожидался 429, получен %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "90" {
		t.Errorf("Retry-After = %q, ожидалось \"90\"", got)
	}
}

// TestFail_Unauthenticated_HTMXvsBrowser — ErrUnauthenticated ведёт себя по-разному:
// htmx получает HX-Redirect+401 (чтобы фронт перешёл на /login без полной перезагрузки),
// обычный браузер — 303 на /login.
func TestFail_Unauthenticated_HTMXvsBrowser(t *testing.T) {
	srv := newBareServer(t)

	// htmx-запрос
	recHX := httptest.NewRecorder()
	reqHX := httptest.NewRequest("GET", "/", nil)
	reqHX.Header.Set("HX-Request", "true")
	srv.fail(recHX, reqHX, domain.ErrUnauthenticated)
	if recHX.Code != http.StatusUnauthorized {
		t.Errorf("htmx: ожидался 401, получен %d", recHX.Code)
	}
	if loc := recHX.Header().Get("HX-Redirect"); loc != "/login" {
		t.Errorf("htmx: HX-Redirect = %q, ожидался /login", loc)
	}

	// обычный браузер
	recBr := httptest.NewRecorder()
	srv.fail(recBr, httptest.NewRequest("GET", "/", nil), domain.ErrUnauthenticated)
	if recBr.Code != http.StatusSeeOther {
		t.Errorf("браузер: ожидался 303, получен %d", recBr.Code)
	}
	if loc := recBr.Header().Get("Location"); loc != "/login" {
		t.Errorf("браузер: Location = %q, ожидался /login", loc)
	}
}

// TestClientIP — security-граница рейт-лимита: доверяем ТОЛЬКО X-Real-IP (его ставит
// наш edge), игнорируем подделываемый X-Forwarded-For, а без заголовков берём
// RemoteAddr фактического соединения.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		realIP     string
		forwarded  string
		remoteAddr string
		want       string
	}{
		{"trust X-Real-IP", "9.9.9.9", "", "10.0.0.1:5555", "9.9.9.9"},
		{"ignore X-Forwarded-For", "", "1.2.3.4", "10.0.0.1:5555", "10.0.0.1"},
		{"X-Real-IP wins over XFF", "9.9.9.9", "1.2.3.4", "10.0.0.1:5555", "9.9.9.9"},
		{"fallback to RemoteAddr", "", "", "203.0.113.7:42000", "203.0.113.7"},
		{"trim spaces in X-Real-IP", "  8.8.8.8 ", "", "10.0.0.1:5555", "8.8.8.8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.realIP != "" {
				req.Header.Set("X-Real-IP", tc.realIP)
			}
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			if got := clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, ожидалось %q", got, tc.want)
			}
		})
	}
}

// errUnexpected — произвольная не-доменная ошибка для проверки ветки default (→ 500).
type errUnexpected string

func (e errUnexpected) Error() string { return string(e) }
