// Тесты клиента платформенного email-API: проверяем КОНТРАКТ запроса к платформе
// (путь, метод, заголовок авторизации, тело) и грейсфул-фолбэк без ключа. Это стык с
// главной платформой — если он поедет, перестанут ходить письма сброса/подтверждения.
package platformmail

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chudno/zerovibe/internal/usecase"
)

func TestSend_PostsToPlatformWithKey(t *testing.T) {
	var (
		gotPath   string
		gotMethod string
		gotKey    string
		gotCT     string
		gotBody   map[string]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotKey = r.Header.Get("X-API-Key")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// apiURL с хвостовым / — клиент должен его срезать, не задвоив путь.
	c := New(srv.URL+"/", "pk_secret")
	err := c.Send(context.Background(), usecase.Email{
		To: "user@example.com", Subject: "Тема", Text: "текст", HTML: "<p>текст</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("метод = %s, ожидался POST", gotMethod)
	}
	if gotPath != "/dev/emails" {
		t.Errorf("путь = %q, ожидался /dev/emails", gotPath)
	}
	if gotKey != "pk_secret" {
		t.Errorf("X-API-Key = %q, ожидался pk_secret", gotKey)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, ожидался application/json", gotCT)
	}
	want := map[string]string{"to": "user@example.com", "subject": "Тема", "text": "текст", "html": "<p>текст</p>"}
	for k, v := range want {
		if gotBody[k] != v {
			t.Errorf("тело[%q] = %q, ожидалось %q", k, gotBody[k], v)
		}
	}
}

func TestSend_ErrorStatusFromPlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests) // напр. дневной лимит исчерпан
	}))
	defer srv.Close()

	c := New(srv.URL, "pk_secret")
	if err := c.Send(context.Background(), usecase.Email{To: "a@b.com", Subject: "s", Text: "t"}); err == nil {
		t.Error("при статусе >= 400 от платформы Send должен вернуть ошибку")
	}
}

// Без ключа (локальная разработка) — грейсфул-фолбэк: письмо логируется, запрос НЕ
// уходит, наружу nil (чтобы флоу сброса работал без настроенной почты). Проверяем,
// что HTTP-вызова не было и письмо попало в лог.
func TestSend_NoKey_LogsAndSkipsHTTP(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var logged string
	c := New(srv.URL, "") // пустой ключ → режим лога
	c.logf = func(format string, args ...any) { logged = format }

	err := c.Send(context.Background(), usecase.Email{To: "a@b.com", Subject: "s", Text: "ссылка-сброса"})
	if err != nil {
		t.Fatalf("без ключа Send должен вернуть nil, получено %v", err)
	}
	if called {
		t.Error("без ключа HTTP-запрос на платформу отправляться не должен")
	}
	if logged == "" {
		t.Error("без ключа письмо должно логироваться")
	}
}
