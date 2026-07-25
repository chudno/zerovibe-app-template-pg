package ycf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
)

// echoMux — приложение-эхо для проверки адаптера: возвращает то, что увидело.
func echoMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hi " + r.URL.Query().Get("name")))
	})
	mux.HandleFunc("POST /form", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		http.SetCookie(w, &http.Cookie{Name: "a", Value: "1"})
		http.SetCookie(w, &http.Cookie{Name: "b", Value: "2"})
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("title=" + r.PostFormValue("title")))
	})
	mux.HandleFunc("GET /cookie", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("session")
		if err != nil {
			http.Error(w, "no cookie", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("session=" + c.Value))
	})
	return mux
}

func call(t *testing.T, ev Event) Response {
	t.Helper()
	raw, _ := json.Marshal(ev)
	out, err := Wrap(echoMux())(context.Background(), raw)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	var resp Response
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("ответ не JSON: %v", err)
	}
	return resp
}

func bodyOf(t *testing.T, resp Response) string {
	t.Helper()
	if !resp.IsBase64Encoded {
		return resp.Body
	}
	b, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		t.Fatalf("тело не base64: %v", err)
	}
	return string(b)
}

func TestGetWithQuery(t *testing.T) {
	resp := call(t, Event{HTTPMethod: "GET", URL: "/hello?name=vibe"})
	if resp.StatusCode != 200 || bodyOf(t, resp) != "hi vibe" {
		t.Fatalf("код=%d тело=%q", resp.StatusCode, bodyOf(t, resp))
	}
}

func TestQueryFromParamsFallback(t *testing.T) {
	// Шлюз может не положить query в url — только в queryStringParameters.
	resp := call(t, Event{HTTPMethod: "GET", URL: "/hello", QueryString: map[string]string{"name": "q"}})
	if bodyOf(t, resp) != "hi q" {
		t.Fatalf("query из параметров потерян: %q", bodyOf(t, resp))
	}
}

func TestPostFormAndMultipleSetCookie(t *testing.T) {
	form := "title=Заметка"
	resp := call(t, Event{
		HTTPMethod: "POST", URL: "/form",
		Headers:         map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:            base64.StdEncoding.EncodeToString([]byte(form)),
		IsBase64Encoded: true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("код=%d", resp.StatusCode)
	}
	if got := bodyOf(t, resp); got != "title=Заметка" {
		t.Fatalf("форма не разобралась: %q", got)
	}
	// ОБЕ Set-Cookie обязаны выжить — одиночная map заголовков их склеила бы.
	cookies := resp.MultiValueHeaders["Set-Cookie"]
	if len(cookies) != 2 {
		t.Fatalf("ждали 2 Set-Cookie, получили %v", cookies)
	}
}

func TestCookieInbound(t *testing.T) {
	resp := call(t, Event{
		HTTPMethod:        "GET",
		URL:               "/cookie",
		MultiValueHeaders: map[string][]string{"Cookie": {"session=tok123"}},
	})
	if resp.StatusCode != 200 || bodyOf(t, resp) != "session=tok123" {
		t.Fatalf("cookie не дошла: код=%d тело=%q", resp.StatusCode, bodyOf(t, resp))
	}
}

func TestNotFoundPassesThrough(t *testing.T) {
	resp := call(t, Event{HTTPMethod: "GET", URL: "/nope"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ждали 404, код=%d", resp.StatusCode)
	}
}

func TestFullURLNormalized(t *testing.T) {
	resp := call(t, Event{HTTPMethod: "GET", URL: "https://fn.example/hello?name=x"})
	if bodyOf(t, resp) != "hi x" {
		t.Fatalf("полный URL не нормализован: %q", bodyOf(t, resp))
	}
}

// Компилируемость сигнатуры обработчика (bytes in/out) — то, что ждёт рантайм.
var _ func(context.Context, []byte) ([]byte, error) = Wrap(echoMux())
