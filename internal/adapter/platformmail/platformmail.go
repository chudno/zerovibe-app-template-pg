// Package platformmail — отправка писем через платформенный email-API Zerovibe.
// Реализует порт usecase.Mailer. Платформа сама подставляет адрес API и сервис-ключ
// в окружение контейнера при деплое; локально/в тестах ключа может не быть — тогда
// письмо логируется (ссылка видна в консоли), а поток не падает.
package platformmail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/chudno/zerovibe/internal/usecase"
)

// Client — отправитель писем поверх платформенного API.
type Client struct {
	apiURL string
	apiKey string
	http   *http.Client
	logf   func(string, ...any)
}

// New собирает клиент. apiURL/apiKey приходят из окружения (PLATFORM_API_URL/KEY).
// Пустой apiKey включает режим лога (см. Send).
func New(apiURL, apiKey string) *Client {
	return &Client{
		apiURL: strings.TrimRight(apiURL, "/"),
		apiKey: apiKey,
		http:   &http.Client{Timeout: 10 * time.Second},
		logf:   log.Printf,
	}
}

// Send отправляет письмо. Без ключа (локальная разработка) — логирует и возвращает
// nil, чтобы восстановление пароля работало в dev без настроенной почты.
func (c *Client) Send(ctx context.Context, m usecase.Email) error {
	if c.apiKey == "" || c.apiURL == "" {
		// Грейсфул-фолбэк ТОЛЬКО для локальной разработки (ключа нет): печатаем письмо
		// со ссылкой в консоль, чтобы можно было пройти флоу без настроенной почты.
		// В проде ключ всегда прокинут платформой → эта ветка не выполняется, тело
		// письма (со ссылкой-токеном) в логи не попадает.
		c.logf("[platformmail] почта не настроена — письмо не отправлено. Кому: %s. Тема: %s. Текст:\n%s",
			m.To, m.Subject, m.Text)
		return nil
	}

	body, err := json.Marshal(map[string]string{
		"to":      m.To,
		"subject": m.Subject,
		"text":    m.Text,
		"html":    m.HTML,
	})
	if err != nil {
		return fmt.Errorf("platformmail: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL+"/dev/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("platformmail: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("platformmail: send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("platformmail: статус %d от email-API", resp.StatusCode)
	}
	return nil
}
