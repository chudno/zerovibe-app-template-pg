// Package ycf — адаптер серверлесс-функции Yandex Cloud к стандартному
// http.Handler приложения.
//
// Функция не слушает порт: платформа вызывает её HTTP-запросом, упакованным в
// JSON-событие (метод, заголовки, query, base64-тело), и ждёт JSON-ответ той же
// формы. Этот пакет разворачивает событие в *http.Request, прогоняет через
// обычный mux приложения и сворачивает ответ обратно. Ни хендлеры, ни шаблоны,
// ни usecase о функции не знают — они видят стандартный net/http.
package ycf

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// Event — HTTP-вызов функции (поля, которые нужны приложению; лишние из
// события игнорируются при разборе).
type Event struct {
	HTTPMethod        string              `json:"httpMethod"`
	URL               string              `json:"url"` // путь с query, напр. "/notes?page=2"
	Headers           map[string]string   `json:"headers"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders"`
	QueryString       map[string]string   `json:"queryStringParameters"`
	Body              string              `json:"body"`
	IsBase64Encoded   bool                `json:"isBase64Encoded"`
}

// Response — ответ функции в формате, который платформа развернёт в HTTP-ответ.
type Response struct {
	StatusCode        int                 `json:"statusCode"`
	Headers           map[string]string   `json:"headers,omitempty"`
	MultiValueHeaders map[string][]string `json:"multiValueHeaders,omitempty"`
	Body              string              `json:"body"`
	IsBase64Encoded   bool                `json:"isBase64Encoded"`
}

// Wrap оборачивает http.Handler приложения в обработчик функции
// (сырые байты события → сырые байты ответа).
func Wrap(h http.Handler) func(ctx context.Context, raw []byte) ([]byte, error) {
	return func(ctx context.Context, raw []byte) ([]byte, error) {
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("ycf: разбор события: %w", err)
		}
		req, err := ToRequest(ctx, ev)
		if err != nil {
			return nil, err
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		out, err := json.Marshal(FromRecorder(rec))
		if err != nil {
			return nil, fmt.Errorf("ycf: сборка ответа: %w", err)
		}
		return out, nil
	}
}

// ToRequest разворачивает событие в стандартный *http.Request.
func ToRequest(ctx context.Context, ev Event) (*http.Request, error) {
	// Путь: поле url ("/notes?page=2"); пустое → корень. Query из url имеет
	// приоритет, queryStringParameters — фолбэк (шлюз может класть только их).
	target := ev.URL
	if target == "" {
		target = "/"
	}
	if !strings.HasPrefix(target, "/") {
		// Полный URL (https://...) — берём путь+query.
		if u, err := url.Parse(target); err == nil {
			target = u.RequestURI()
		}
	}
	if !strings.Contains(target, "?") && len(ev.QueryString) > 0 {
		q := url.Values{}
		for k, v := range ev.QueryString {
			q.Set(k, v)
		}
		target += "?" + q.Encode()
	}

	var body []byte
	if ev.Body != "" {
		if ev.IsBase64Encoded {
			b, err := base64.StdEncoding.DecodeString(ev.Body)
			if err != nil {
				return nil, fmt.Errorf("ycf: base64-тело: %w", err)
			}
			body = b
		} else {
			body = []byte(ev.Body)
		}
	}

	method := ev.HTTPMethod
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(body)).WithContext(ctx)

	// Заголовки: multiValueHeaders полнее (повторы, напр. несколько Cookie);
	// одиночные — фолбэк.
	if len(ev.MultiValueHeaders) > 0 {
		for k, vs := range ev.MultiValueHeaders {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	} else {
		for k, v := range ev.Headers {
			req.Header.Set(k, v)
		}
	}
	if host := req.Header.Get("Host"); host != "" {
		req.Host = host
	}
	return req, nil
}

// FromRecorder сворачивает записанный ответ приложения в Response.
// Тело всегда base64 (безопасно для любого контента: HTML, картинки, gzip);
// Set-Cookie идут через multiValueHeaders — их может быть несколько, а
// одиночная map их склеила бы.
func FromRecorder(rec *httptest.ResponseRecorder) Response {
	return Response{
		StatusCode:        rec.Code,
		MultiValueHeaders: rec.Header(),
		Body:              base64.StdEncoding.EncodeToString(rec.Body.Bytes()),
		IsBase64Encoded:   true,
	}
}
